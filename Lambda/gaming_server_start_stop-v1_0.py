# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0


import boto3
import json
import os
import shlex
import time


def lambda_handler(event, context): #standard function called on lambda invocation
    
    tagKey = event['tagName'] #This is the Tag for the resources we're looking to handle
    tagValue = event['tagValue'] #This is the Tag for the resources we're looking to handle
    targetInstanceId = event.get('instanceId') #Optional - restricts start/stop/resize to a single instance instead of all tagged instances
    global ec2
    global s3
    instanceIds = []
    info = []
    serverResizeCheck = "OK"
    statemachineresponse = {}

    ec2 = boto3.client('ec2') #Sets up ec2 as the object to call the boto3 (AWS Python SDK) client library for the EC2 service
    s3 = boto3.client('s3')
    global backup
    backup = boto3.client('backup')
    global ssm
    ssm = boto3.client('ssm')
    global events
    events = boto3.client('events')
    info = getInfo(tagKey, tagValue)
    
    if len(info['Instances']) < 1:
        statusmessage = "No gaming server instances found" #sets errormessage variable to error text as shown
        return(statusmessage)

    #Determine which instances the start/stop/resize action should actually apply to.
    #If instanceId was supplied, restrict to that single instance; otherwise fall back to all tagged instances.
    if targetInstanceId:
        targetInstances = [i for i in info['Instances'] if i['InstanceId'] == targetInstanceId]
        if len(targetInstances) < 1:
            statusmessage = "Requested instanceId was not found among your gaming server instances"
            return(statusmessage, info)
    else:
        targetInstances = info['Instances']

    for i in targetInstances:
        foundInstanceId = i['InstanceId']
        instanceIds.append(foundInstanceId)

    #Two-tier access control: "admins" can do everything below; "users" are limited to Start/Stop and
    #Download World, and only for games whose server-stack.yaml opted in via AllowUserLifecycle/
    #AllowUserDownloads (see UserLifecycleAllowed/UserDownloadsAllowed in getInfo() above). The caller's
    #Cognito groups arrive here as a plain string via API Gateway's VTL request template
    #($context.authorizer.claims.get('cognito:groups')) - API Gateway has historically rendered a
    #multi-valued claim like this as "[admins, users]" rather than valid JSON, so strip any brackets
    #before splitting rather than assuming a clean comma list.
    groups = [g.strip() for g in (event.get('groups') or '').strip('[]').split(',') if g.strip()]
    isAdmin = 'admins' in groups
    command = event['command']
    if not isAdmin:
        if command in ("start", "stop"):
            permitted = targetInstanceId and any(i['InstanceId'] == targetInstanceId and i.get('UserLifecycleAllowed') for i in targetInstances)
            if not permitted:
                return ("You don't have permission to start/stop this server", info)
        elif command in ("startWorldDownload", "getWorldDownloadStatus"):
            permitted = targetInstanceId and any(i['InstanceId'] == targetInstanceId and i.get('UserDownloadsAllowed') for i in targetInstances)
            if not permitted:
                return ("You don't have permission to download this server's world", info)
        elif command != "getInfo":
            #covers reSize/backupNow/pauseIdleShutdown/resumeIdleShutdown/installMod/deleteMod - installing
            #or removing a mod rewrites the server's actual content, which is closer to Resize/Backup Now
            #than to a per-user-taggable action like Start/Stop or Download World (see
            #docs/frontend-ui-ux-requirements.md's "Install/delete mods" section).
            return ("You don't have permission to perform this action", info)

    if event['command'] == "start":
        try:
            ec2.start_instances(InstanceIds=instanceIds)
            statusmessage = "If this message appears, something has gone very wrong"
        except:
            print("start failed")
            statusmessage = "Couldn't start server, please try again later"
            return(statusmessage,info)
        #the idle-check tag (see IdleShutdownLambda in cfn/server-stack.yaml) persists across a stop/start
        #cycle since it's just an EC2 tag, not tied to the instance's own lifecycle - if left in place, the
        #very next automatic idle check after this start would find it still set and immediately re-stop
        #the instance, treating "first check since restart" as "second consecutive empty check". Clear it
        #on every start (regardless of what stopped it, or why) so idle-detection always begins fresh.
        try:
            ec2.delete_tags(Resources=instanceIds, Tags=[{'Key': 'idle-check'}])
        except Exception as e:
            print("Couldn't clear idle-check tag: "+str(e))
        try:
            statemachineresponse = updateDnsStateFunc({'Instances': targetInstances})
            print(statemachineresponse)
            statusmessage = "Started server and updated DNS successfully"
        except:
            statusmessage = "Server started, but DNS update failed - please wait a few minutes and try again or check your hosted zone is setup correctly"
    elif event['command'] == "stop":
        try:
            ec2.stop_instances(InstanceIds=instanceIds)
            statusmessage = "Stopped server"
        except:
            statusmessage = "Stopping server failed - please wait a few minutes and try again"
            return(statusmessage,info)
        #same reasoning as the "start" branch above: clear idle-check here too, so a manual stop never
        #leaves a stale idle-check=true tag behind that would show "Idle - stopping soon" on an
        #already-stopped instance.
        try:
            ec2.delete_tags(Resources=instanceIds, Tags=[{'Key': 'idle-check'}])
        except Exception as e:
            print("Couldn't clear idle-check tag: "+str(e))
    elif event['command'] == "getInfo":
            statusmessage = "No action, just getting info"
    elif event['command'] == "reSize":
        for i in targetInstances:
            if i['State'] != "stopped":
                statusmessage = "Your server is not stopped. Please stop it and retry resizing"
                return (statusmessage,info)
            try:
                for i in instanceIds:
                    try:
                        ec2.modify_instance_attribute(
                            InstanceId=i,
                            InstanceType={'Value':  os.environ[event['reSizeType']]},
                        )
                    except:
                        serverResizeCheck = "NOK"
                if serverResizeCheck == "OK":
                    statusmessage = "Server has been resized - please note it is currently stopped."
                else:
                    statusmessage = "There was an issue resizing your server.  Make sure your target instance type is compatible (e.g. ARM bases servers such as T3g servers cannot be resized to x86 server types such as T3a servers"
            except:
                    statusmessage = "Something went wrong with resizing your server, please try again later"
    elif event['command'] == "backupNow":
        #triggers an on-demand AWS Backup job - same mechanism as the daily scheduled one, just kicked off
        #immediately. This is a genuine full-volume backup (includes serverconfig.json, logs, everything -
        #not just the essential world data), so it keeps "Backup" naming throughout, distinct from
        #"Download World" (startWorldDownload/getWorldDownloadStatus below), which is deliberately
        #filtered to just the essential world data and excludes anything like serverconfig.json's password.
        accountId = context.invoked_function_arn.split(':')[4]
        started = []
        failed = []
        for i in targetInstances:
            gameName = i.get('GameName')
            if not gameName:
                failed.append(i['InstanceId'])
                continue
            try:
                startBackupNow(gameName, accountId)
                started.append(gameName)
            except Exception as e:
                print("backupNow failed for "+str(gameName)+": "+str(e))
                failed.append(gameName)
        if started and not failed:
            statusmessage = "Backup started for " + ", ".join(started)
        elif started and failed:
            statusmessage = "Backup started for " + ", ".join(started) + ", but failed for " + ", ".join(failed)
        else:
            statusmessage = "Couldn't start backup, please try again later"
    elif event['command'] == "pauseIdleShutdown" or event['command'] == "resumeIdleShutdown":
        #pauses/resumes the per-server idle-detection schedule (see IdleCheckSchedule in
        #cfn/server-stack.yaml) - e.g. for maintenance, where you want the instance to stay up regardless
        #of player count. Doesn't affect the manual Stop button or the AWS Backup schedule, only this.
        pausing = event['command'] == "pauseIdleShutdown"
        done = []
        for i in targetInstances:
            gameName = i.get('GameName')
            if not gameName:
                continue
            try:
                ruleName = 'game-server-'+gameName+'-cfn-idle-check'
                if pausing:
                    events.disable_rule(Name=ruleName)
                else:
                    events.enable_rule(Name=ruleName)
                done.append(gameName)
            except Exception as e:
                print(event['command']+" failed for "+str(gameName)+": "+str(e))
        verb = "paused" if pausing else "resumed"
        statusmessage = "Auto-shutdown "+verb+" for " + ", ".join(done) if done else "Couldn't "+("pause" if pausing else "resume")+" auto-shutdown, please try again later"
    elif event['command'] == "startWorldDownload":
        #returns a different shape than the other commands (no [statusmessage, info] tuple) - the front
        #end handles this one specially, polling getWorldDownloadStatus rather than showing an alert
        if not targetInstances:
            return {"status": "Failed", "error": "No matching instance found"}
        i = targetInstances[0]
        gameName = i.get('GameName')
        dataPath = i.get('DataPath')
        #"core" (Download Just World) vs "full" (Download All Data) - see
        #docs/frontend-ui-ux-requirements.md. Anything other than exactly "core" defaults to full, matching
        #the front-end's own fallback and keeping this backward compatible with any other caller.
        scope = 'core' if event.get('scope') == 'core' else 'full'
        includePaths = i.get('WorldDownloadCorePaths') if scope == 'core' else i.get('WorldDownloadPaths')
        if not gameName or not dataPath or not includePaths:
            return {"status": "Failed", "error": "Instance is missing its game-name/data-path/world-download-paths tag"}
        try:
            return startWorldDownload(i['InstanceId'], gameName, dataPath, includePaths, scope)
        except Exception as e:
            print("startWorldDownload failed: "+str(e))
            return {"status": "Failed", "error": "Could not start the world download"}
    elif event['command'] == "getWorldDownloadStatus":
        commandId = event.get('commandId')
        s3Key = event.get('s3Key')
        if not targetInstances or not commandId or not s3Key:
            return {"status": "Failed", "error": "Missing commandId/instanceId/s3Key"}
        targetInstance = targetInstances[0]
        #s3Key is client-supplied and otherwise never cross-checked against instanceId - without this, a
        #caller with download rights on ANY one game (a valid commandId/instanceId pair of their own to
        #satisfy the ssm.get_command_invocation lookup below) could substitute a different game's s3Key and
        #get a presigned URL to a world zip they have no download permission for. Must match exactly what
        #startWorldDownload() itself constructs the key as.
        expectedPrefix = 'worlds/'+str(targetInstance.get('GameName'))+'/'+targetInstance['InstanceId']+'-'
        if not s3Key.startswith(expectedPrefix):
            return {"status": "Failed", "error": "s3Key does not belong to this instance"}
        return getWorldDownloadStatus(commandId, targetInstance['InstanceId'], s3Key)
    elif event['command'] == "installMod":
        if not targetInstances:
            return ("No matching instance found", info)
        i = targetInstances[0]
        gameName = i.get('GameName')
        dataPath = i.get('DataPath')
        modref = event.get('modref')
        if not gameName or not dataPath or not modref:
            return ("Missing instance/modref", info)
        #Vintage Story's moddb requires the server's own game version as an explicit argument - confirmed
        #live: omitting it makes the server try to look up "current" versions itself via a request that's
        #been failing (a JSON parsing error inside the game's own moddb client). GameStatus.version is
        #already reported by the status agent for exactly this kind of use.
        gameVersion = (i.get('GameStatus') or {}).get('version')
        try:
            statusmessage = installMod(i['InstanceId'], gameName, dataPath, modref, gameVersion)
        except Exception as e:
            print("installMod failed: "+str(e))
            statusmessage = "Couldn't install mod, please try again later"
        return (statusmessage, info)
    elif event['command'] == "deleteMod":
        if not targetInstances:
            return ("No matching instance found", info)
        i = targetInstances[0]
        gameName = i.get('GameName')
        dataPath = i.get('DataPath')
        modname = event.get('modname')
        if not gameName or not dataPath or not modname:
            return ("Missing instance/modname", info)
        try:
            statusmessage = deleteMod(i['InstanceId'], gameName, dataPath, modname)
        except Exception as e:
            print("deleteMod failed: "+str(e))
            statusmessage = "Couldn't delete mod, please try again later"
        return (statusmessage, info)
    elif event['command'] == "restartGame":
        #restarts just the game process/container, not the EC2 instance - e.g. to pick up a manually
        #installed mod or a hand-edited config, without the ~minutes-long cost of a full instance
        #stop/start. Reuses the same per-game restart mechanism installMod/deleteMod already use.
        if not targetInstances:
            return ("No matching instance found", info)
        i = targetInstances[0]
        gameName = i.get('GameName')
        dataPath = i.get('DataPath')
        if not gameName or not dataPath:
            return ("Missing instance", info)
        try:
            statusmessage = restartGame(i['InstanceId'], gameName, dataPath)
        except Exception as e:
            print("restartGame failed: "+str(e))
            statusmessage = "Couldn't restart the game, please try again later"
        return (statusmessage, info)
    elif event['command'] == "updateGame":
        #updates the game server software itself, not the EC2 instance. Both games' update mechanism
        #already restarts the game as part of doing so - no separate restartGame call needed after this.
        if not targetInstances:
            return ("No matching instance found", info)
        i = targetInstances[0]
        gameName = i.get('GameName')
        dataPath = i.get('DataPath')
        if not gameName or not dataPath:
            return ("Missing instance", info)
        try:
            statusmessage = updateGame(i['InstanceId'], gameName, dataPath)
        except Exception as e:
            print("updateGame failed: "+str(e))
            statusmessage = "Couldn't update the game, please try again later"
        return (statusmessage, info)
    else:
        statusmessage = "Error - invalid invocation event received"
    return(statusmessage,info)

def getInfo(tagKey, tagValue):
    info = json.loads('{"Instances":[]}')
    filter =[{'Name': 'tag:'+tagKey, 'Values': [tagValue]}]
    response = ec2.describe_instances(Filters=filter)
    for reservation in response["Reservations"]: #starts for loop for all reservations returned
        for instance in reservation["Instances"]:
            if instance['State'].get('Name') != 'terminated':
                infoDict = {}
                infoDict['InstanceId'] = instance['InstanceId']
                infoDict['InstanceType'] = instance['InstanceType']
                infoDict['State'] = instance['State'].get('Name')
                for i in instance['Tags']:
                    if(i.get('Key') == 'domain'):
                        infoDict['DomainName'] = i.get('Value','No domain value found')
                        break
                    else:
                        infoDict['DomainName'] = 'No domain tag found' 
                for i in instance['Tags']:
                    if(i.get('Key') == 'hostedZoneId'):
                        infoDict['hostedZoneId'] = i.get('Value','No hosted zone value found')
                        break
                    else:
                        infoDict["hostedZoneId"] = 'No hosted zone tag found'
                infoDict['PublicIpAddress'] = instance.get('PublicIpAddress','No public IP address')
                gameName = next((i.get('Value') for i in instance['Tags'] if i.get('Key') == 'game-name'), None)
                #resolved server-side from the instance's own tags (not trusted from the client) so
                #backupNow/startWorldDownload always operate on the game the instance actually is
                infoDict['GameName'] = gameName
                infoDict['DataPath'] = next((i.get('Value') for i in instance['Tags'] if i.get('Key') == 'data-path'), None)
                infoDict['WorldDownloadPaths'] = next((i.get('Value') for i in instance['Tags'] if i.get('Key') == 'world-download-paths'), None)
                infoDict['WorldDownloadCorePaths'] = next((i.get('Value') for i in instance['Tags'] if i.get('Key') == 'world-download-core-paths'), None)
                infoDict['GameStatus'] = getGameStatus(gameName) if infoDict['State'] == 'running' else None
                #promoted out of GameStatus to a top-level field per docs/frontend-ui-ux-requirements.md -
                #the front-end's Server Name column/field expects it there, not nested
                infoDict['ServerName'] = infoDict['GameStatus'].get('serverName') if infoDict['GameStatus'] else None
                #independent of instance State - AWS Backup runs on its own daily schedule against the
                #EBS data volume regardless of whether the instance itself is stopped
                infoDict['LastBackupTime'] = getLastBackupTime(gameName)
                infoDict['IdleShutdownStatus'] = getIdleShutdownStatus(gameName, instance.get('Tags', []))
                #per-game toggles (cfn/server-stack.yaml's AllowUserLifecycle/AllowUserDownloads) for
                #whether the "users" Cognito group - not just "admins" - may Start/Stop or Download World
                #for this specific game. Resolved here from the instance's own tags so both the
                #authorization check below and the front-end's button visibility read the same values.
                infoDict['UserLifecycleAllowed'] = next((i.get('Value') == 'true' for i in instance['Tags'] if i.get('Key') == 'user-lifecycle-allowed'), False)
                infoDict['UserDownloadsAllowed'] = next((i.get('Value') == 'true' for i in instance['Tags'] if i.get('Key') == 'user-downloads-allowed'), False)
                info["Instances"].append(infoDict)
    return(info)

def getGameStatus(gameName):
    #Reads the per-server status snapshot (players/version/mods) that game's status agent pushes to S3 - see docs/server-status.md
    statusBucket = os.environ.get('statusBucket')
    if not statusBucket or not gameName:
        return None
    try:
        obj = s3.get_object(Bucket=statusBucket, Key='status/'+gameName+'.json')
        return json.loads(obj['Body'].read())
    except Exception as e:
        print("No game status available for "+str(gameName)+": "+str(e))
        return None

def getLastBackupTime(gameName):
    #Vault name follows the fixed convention set in cfn/server-stack.yaml (BackupVaultName: "${AWS::StackName}-backup-vault-daily",
    #where StackName is "game-server-<gameName>-cfn"), so it can be derived here without a lookup.
    if not gameName:
        return None
    vaultName = 'game-server-' + gameName + '-cfn-backup-vault-daily'
    try:
        points = backup.list_recovery_points_by_backup_vault(BackupVaultName=vaultName)['RecoveryPoints']
        completed = [p['CreationDate'] for p in points if p.get('Status') == 'COMPLETED']
        return max(completed).strftime('%Y-%m-%dT%H:%M:%SZ') if completed else None
    except Exception as e:
        print("No backup info available for "+str(gameName)+": "+str(e))
        return None

def getIdleShutdownStatus(gameName, tags):
    #Three states, not just enabled/disabled: "disabled" (paused, e.g. for maintenance), "enabled" (the
    #15-min schedule is running and hasn't seen an idle check yet), or "triggered-once" (one empty check
    #already recorded via the idle-check tag - see IdleShutdownLambda in cfn/server-stack.yaml - so the
    #*next* empty check will actually stop the instance). The tag data is already in hand from getInfo()'s
    #own describe_instances call - only the rule's enabled/disabled state needs a fresh lookup.
    if not gameName:
        return None
    try:
        ruleState = events.describe_rule(Name='game-server-'+gameName+'-cfn-idle-check')['State']
    except Exception as e:
        print("No idle-shutdown rule info available for "+str(gameName)+": "+str(e))
        return None
    if ruleState != 'ENABLED':
        return 'disabled'
    wasIdle = any(t.get('Key') == 'idle-check' and t.get('Value') == 'true' for t in tags)
    return 'triggered-once' if wasIdle else 'enabled'

def startBackupNow(gameName, accountId):
    #Same call already proven manually: back up the data volume specifically (not the whole instance) -
    #looked up by its game-name tag, since there's exactly one live data volume per game at a time.
    #Vault name and the backup role's name both follow the fixed conventions set in cfn/server-stack.yaml
    #(confirmed live that resourcegroupstaggingapi doesn't index IAM roles at all in this account, so a
    #tag-based lookup isn't an option - the role's name has to be deterministic instead).
    region = os.environ.get('AWS_REGION')
    volumes = ec2.describe_volumes(Filters=[{'Name': 'tag:game-name', 'Values': [gameName]}])['Volumes']
    if not volumes:
        raise Exception("No data volume found for "+gameName)
    volumeArn = 'arn:aws:ec2:'+region+':'+accountId+':volume/'+volumes[0]['VolumeId']
    vaultName = 'game-server-'+gameName+'-cfn-backup-vault-daily'
    roleArn = 'arn:aws:iam::'+accountId+':role/service-role/game-server-'+gameName+'-cfn-backup-role'
    backup.start_backup_job(
        BackupVaultName=vaultName,
        ResourceArn=volumeArn,
        IamRoleArn=roleArn,
    )

def startWorldDownload(instanceId, gameName, dataPath, includePaths, scope):
    #Zips specific subfolders of the currently-live, currently-mounted data directory (not the whole
    #thing - e.g. Vintage Story's serverconfig.json has a Password field in it that has no business in a
    #downloadable file) via SSM Run Command and uploads the result to the shared status bucket under a
    #worlds/ prefix. Only works while the instance is running. includePaths is a comma-separated list of
    #paths relative to dataPath (world-download-paths or world-download-core-paths, depending on scope -
    #both tags set per-game in cfn/server-stack.yaml) - kept driven by tags rather than hardcoded here so
    #this stays game-agnostic. The S3 key is built up front, not parsed out of the command's own output
    #later, which would be fragile. instanceId immediately follows the prefix (not scope) so
    #getWorldDownloadStatus's ownership check (expectedPrefix, above) doesn't need to change per scope.
    bucket = os.environ.get('statusBucket')
    key = 'worlds/'+gameName+'/'+instanceId+'-'+scope+'-'+str(int(time.time()))+'.zip'
    folders = ' '.join('"'+p.strip()+'"' for p in includePaths.split(','))
    commands = [
        #tolerates any of the listed folders not existing yet (e.g. a brand new world may have no
        #Playerdata folder) - a straight `zip -r ... f1 f2 f3` would otherwise fail the whole command
        'cd '+dataPath+' && for p in '+folders+'; do [ -e "$p" ] && zip -r /tmp/worldsave.zip "$p"; done',
        'aws s3 cp /tmp/worldsave.zip s3://'+bucket+'/'+key,
        'rm -f /tmp/worldsave.zip',
    ]
    #default TimeoutSeconds is 3600 if unset - too short for a multi-GB world; SSM's own max is 172800 (48h)
    response = ssm.send_command(
        InstanceIds=[instanceId],
        DocumentName='AWS-RunShellScript',
        Parameters={'commands': commands},
        TimeoutSeconds=14400,
    )
    return {"status": "InProgress", "commandId": response['Command']['CommandId'], "s3Key": key}

def getWorldDownloadStatus(commandId, instanceId, s3Key):
    bucket = os.environ.get('statusBucket')
    try:
        result = ssm.get_command_invocation(CommandId=commandId, InstanceId=instanceId)
    except ssm.exceptions.InvocationDoesNotExist:
        #can happen briefly right after send_command, before the command has actually registered on the
        #instance - not a real failure, just keep polling
        return {"status": "InProgress"}
    status = result['Status']
    if status in ('Pending', 'InProgress', 'Delayed'):
        return {"status": "InProgress"}
    if status == 'Success':
        #generating this URL is a local signing operation and succeeds regardless of the signer's actual
        #permissions - the s3:GetObject grant on this Lambda's role is what makes the URL actually work
        #when the browser follows it, enforced at click-time, not here
        #
        #s3Key is worlds/<gameName>/<instanceId>-<scope>-<epoch>.zip (see startWorldDownload) - parsed back
        #out here to build a human-readable download filename (real date/time instead of a raw epoch) via
        #Content-Disposition, decoupled from the S3 key's own naming, which stays optimized for
        #uniqueness/organization rather than readability.
        gameName = s3Key.split('/')[1] if s3Key.count('/') >= 1 else 'server'
        try:
            suffix = s3Key.rsplit('/', 1)[-1][len(instanceId)+1:-4]  # "<scope>-<epoch>"
            scope, epochStr = suffix.rsplit('-', 1)
            timestamp = time.strftime('%Y%m%d-%H%M%S', time.gmtime(int(epochStr)))
            filename = '{}-{}-{}.zip'.format(gameName, scope, timestamp)
        except Exception:
            filename = s3Key.rsplit('/', 1)[-1]
        url = s3.generate_presigned_url('get_object', Params={
            'Bucket': bucket,
            'Key': s3Key,
            'ResponseContentDisposition': 'attachment; filename="{}"'.format(filename),
        }, ExpiresIn=900)
        return {"status": "Success", "url": url}
    return {"status": "Failed", "error": result.get('StandardErrorContent', '')[:500]}

#Vintage Story mods live directly under DataPath/Mods (status_agent.py's own hardcoded MODS_PATH
#convention - never tag-driven, so no new server-stack.yaml parameter needed here either).
VS_MODS_SUBDIR = "Mods"

#Resolves a mod's *display* name (what deleteMod's caller has, from GameStatus.mods - see
#docs/server-status.md) to its modid (what /moddb remove actually needs - confirmed live these are
#different: e.g. display name "Carry Capacity" vs modid "carrycapacity"). Scans the Mods folder and
#re-derives each entry's display name exactly the way status_agent.py's read_mods() already does (a zip's
#modinfo.json vs. an extracted folder's modinfo.json), so this matches the same entry the front-end is
#showing, without duplicating a second name-matching implementation that could drift out of sync. Prints
#the resolved modid (not the display name) on success so the calling shell script can feed it straight to
#/moddb remove.
VS_MOD_FIND_MODID_SCRIPT = r'''python3 - <<'PYEOF'
import json, os, sys, zipfile

MODS_PATH = {modsPath}
target = {modname}

def read_info(read_fn):
    try:
        return json.loads(read_fn())
    except Exception:
        return None

modid = None
if os.path.isdir(MODS_PATH):
    for entry in os.listdir(MODS_PATH):
        path = os.path.join(MODS_PATH, entry)
        info = None
        if entry.lower().endswith(".zip"):
            try:
                with zipfile.ZipFile(path) as zf:
                    info = read_info(lambda: zf.read("modinfo.json").decode("utf-8"))
            except Exception:
                pass
        elif os.path.isdir(path):
            modinfo_path = os.path.join(path, "modinfo.json")
            if os.path.exists(modinfo_path):
                info = read_info(lambda: open(modinfo_path).read())
        if info and (info.get("name") or info.get("Name")) == target:
            modid = info.get("modid") or info.get("ModID") or info.get("Modid")
            break

if not modid:
    print("NO_MATCH")
    sys.exit(1)
print(modid)
PYEOF'''

def installMod(instanceId, gameName, dataPath, modref, gameVersion=None):
    #Both games' actual mod install has nothing to reimplement - Vintage Story has a built-in console
    #command that resolves/downloads/validates (confirmed live: /moddb install <modid> <gameVersion> - the
    #gameVersion argument is required, not optional: without it the server tries to look up "current"
    #versions itself via a request that's been failing with a JSON parsing error inside the game's own
    #moddb client), and Valheim's Odin already does the same from a MODS= list on every container start
    #(the exact mechanism already running the mods installed manually earlier this session).
    #
    #Deliberately does NOT restart the game - confirmed with you: install/delete should never restart on
    #their own. An admin installing several mods in a row would otherwise pay a restart per mod; instead
    #they call the separate restartGame command once, whenever they're actually ready to load everything
    #installed/removed so far.
    if gameName == 'vintagestory':
        if not gameVersion:
            return "Couldn't determine the server's current game version - try again once the status agent has reported it"
        serverCmd = 'moddb install ' + modref + ' ' + gameVersion
        commands = [
            'sudo -u vintagestory /home/vintagestory/server/server.sh command ' + shlex.quote(serverCmd),
        ]
    elif gameName == 'valheim':
        composeFile = dataPath + '/docker-compose.yml'
        script = VALHEIM_MOD_INSERT_SCRIPT.format(path=repr(composeFile), modref=repr(modref))
        commands = [
            #MODS only takes effect with TYPE=BepInEx already set (confirmed live earlier this session -
            #Odin silently no-ops MODS entirely otherwise) - fail clearly and never touch the file if this
            #isn't set, per the explicit decision not to auto-enable BepInEx.
            'grep -q "^ *- TYPE=BepInEx$" ' + shlex.quote(composeFile) + ' || { echo "BepInEx is not enabled on this server - set TYPE=BepInEx in docker-compose.yml first" >&2; exit 1; }',
            script,
        ]
    else:
        return "Mod install isn't supported for "+str(gameName)
    print("installMod: instanceId="+instanceId+" gameName="+gameName+" modref="+modref)
    success, stdout, stderr = runSsmCommandSync(instanceId, commands)
    #logged regardless of success - a shell exit code of 0 only means the commands themselves ran without
    #a shell-level error, NOT that the game's own console command actually did anything (confirmed live:
    #injecting a console command "succeeds" trivially every time, independent of whether the game itself
    #installed the mod) - this is the one place that can actually show what the game's console printed
    #back, until a real success/failure check replaces trusting the exit code.
    print("installMod result: success="+str(success)+" stdout="+repr(stdout)+" stderr="+repr(stderr))
    if success:
        return "Mod install requested - restart the server (once you're ready) to load it"
    return "Couldn't install mod: "+(stderr or stdout or "unknown error")[:300]

def deleteMod(instanceId, gameName, dataPath, modname):
    #Deliberately does NOT restart the game either - same reasoning as installMod: an admin removing
    #several mods (or one of each) shouldn't pay a restart per action, only once via the separate
    #restartGame command when they're actually ready.
    if gameName == 'vintagestory':
        #uses the game's own /moddb remove <modid> (confirmed live) rather than deleting the mod's
        #file/folder directly - modname (the display name from GameStatus.mods) isn't what /moddb remove
        #needs, so VS_MOD_FIND_MODID_SCRIPT resolves display name -> modid first, same matching logic as
        #before, just repurposed to look up rather than delete.
        findScript = VS_MOD_FIND_MODID_SCRIPT.format(modsPath=repr(dataPath+'/'+VS_MODS_SUBDIR), modname=repr(modname))
        commands = [
            #the heredoc's closing PYEOF must be alone on its own line for bash to recognize it as the
            #terminator - the trailing "\n" before the closing ")" is required, not cosmetic; without it
            #bash can't find a valid terminator line and silently reads to true end-of-script instead
            #(confirmed locally: it happened to still work by coincidence, with a "here-document delimited
            #by end-of-file" warning - too fragile to rely on).
            'MODID=$(' + findScript + '\n)',
            #the find script's own "NO_MATCH" print gets swallowed by the $(...) capture above (it becomes
            #MODID's value, not part of this script's own stdout) - re-echo it so the failure-message
            #check below (looking for "NO_MATCH" in the overall command's captured stdout) still works.
            'if [ $? -ne 0 ]; then echo "NO_MATCH"; exit 1; fi',
            'sudo -u vintagestory /home/vintagestory/server/server.sh command "moddb remove $MODID"',
        ]
    elif gameName == 'valheim':
        composeFile = dataPath + '/docker-compose.yml'
        script = VALHEIM_MOD_DELETE_SCRIPT.format(path=repr(composeFile), modname=repr(modname))
        commands = [
            script,
        ]
    else:
        return "Mod delete isn't supported for "+str(gameName)
    print("deleteMod: instanceId="+instanceId+" gameName="+gameName+" modname="+modname)
    success, stdout, stderr = runSsmCommandSync(instanceId, commands)
    print("deleteMod result: success="+str(success)+" stdout="+repr(stdout)+" stderr="+repr(stderr))
    if success:
        return "Mod removed - restart the server (once you're ready) for the change to take effect"
    if 'NO_MATCH' in stdout:
        return "Couldn't find a mod matching \""+modname+"\" to delete"
    return "Couldn't delete mod: "+(stderr or stdout or "unknown error")[:300]

def restartGame(instanceId, gameName, dataPath):
    #just the game process/container, not the EC2 instance - the only place that actually restarts either
    #game now (installMod/deleteMod deliberately don't - see their own comments). Confirmed live per-game
    #mechanism: server.sh restart for Vintage Story; docker compose down/up for Valheim, consistent with
    #its own graceful-stop timing elsewhere in this file. Backgrounded (setsid, redirected fds) since a
    #real restart can take a while - Valheim's own graceful-stop grace period alone is 60s - and this SSM
    #call should stay fast regardless.
    if gameName == 'vintagestory':
        commands = [
            'setsid sh -c "sudo -u vintagestory /home/vintagestory/server/server.sh restart" </dev/null >/dev/null 2>&1 &',
        ]
    elif gameName == 'valheim':
        commands = [
            'setsid sh -c "cd ' + shlex.quote(dataPath) + ' && docker compose down && docker compose up -d" </dev/null >/dev/null 2>&1 &',
        ]
    else:
        return "Restart isn't supported for "+str(gameName)
    print("restartGame: instanceId="+instanceId+" gameName="+gameName)
    success, stdout, stderr = runSsmCommandSync(instanceId, commands)
    print("restartGame result: success="+str(success)+" stdout="+repr(stdout)+" stderr="+repr(stderr))
    if success:
        return "Restart requested - the server will be back in a minute or two"
    return "Couldn't restart the game: "+(stderr or stdout or "unknown error")[:300]

#Vintage Story's own server.sh `update` subcommand turned out to be a permanently disabled stub (confirmed
#live via its source: it just prints manual instructions and exits 0 - "Auto update is disabled because
#unreliable") rather than a real update mechanism, unlike restart/moddb. Those printed instructions are
#close to what scripts/VintageStory/install.sh itself already does for a fresh install (stop, wget the
#tarball, extract, chmod, patch DATAPATH, restart) - this reimplements that same sequence for an in-place
#update, resolving the target version live from the game's own public gameversions API instead of a
#hardcoded VSVERSION pin. Written to a script file on the instance rather than inlined into the SSM command
#text (like the mod scripts) since it's long and multi-step; output goes to a log file rather than
#/dev/null so a failure partway through is actually diagnosable.
VS_UPDATE_SCRIPT = r'''#!/bin/bash
set -e

#jq is already installed (see install.sh's apt line) - filters out pre-release tags like "1.4.4-dev.2" the
#same way docs/frontend-ui-ux-requirements.md's own research flagged, taking the newest stable entry
VERSION=$(curl -fsS https://mods.vintagestory.at/api/gameversions | jq -r '[.gameversions[].name | select(contains("-") | not)] | last')
if [ -z "$VERSION" ] || [ "$VERSION" = "null" ]; then
  echo "Couldn't determine the latest available version" >&2
  exit 1
fi
echo "Updating Vintage Story server to $VERSION"

sudo -u vintagestory /home/vintagestory/server/server.sh stop

#confirmed live: extracting straight over an existing install leaves old and new asset files mixed
#together, and the game refuses to start as a safety check ("Your Server installation still contains old
#files from a previous game version... Please fully delete the /assets folder and then do a full
#reinstallation") - this is exactly what the disabled vs_update stub's own instructions said to do (rm -rf
#*) that got missed on the first version of this script. /home/vintagestory/server only ever holds the
#ephemeral game binary (same as install.sh's own fresh-install comment notes) - DataPath is a separate
#top-level directory, never nested under here, so wiping this is safe
sudo rm -rf /home/vintagestory/server/*

cd /tmp
sudo wget -O vs_server_update.tar.gz "https://cdn.vintagestory.at/gamefiles/stable/vs_server_linux-x64_${VERSION}.tar.gz"
sudo tar -C /home/vintagestory/server -xzf vs_server_update.tar.gz
#confirmed live: the tarball's owned by root (sudo wget), so a plain rm -f here fails with "Operation not
#permitted" - harmless (just a stray file left in /tmp), but sudo is the actual fix
sudo rm -f vs_server_update.tar.gz

#scoped to server/ only, not all of /home/vintagestory like install.sh's own chown -R (fine there since the
#data volume isn't populated yet at first install) - an in-place update must not touch the persistent
#world-save data under DataPath
sudo chown -R vintagestory:vintagestory /home/vintagestory/server
sudo chmod +x /home/vintagestory/server/VintagestoryServer /home/vintagestory/server/server.sh

#the freshly-extracted server.sh ships install.sh's same shared-default DATAPATH - reapply the same patch
#install.sh applies on first install, or the update silently points the server back at the wrong data path
sudo sed -i "s|^DATAPATH='/var/vintagestory/data'|DATAPATH='__DATAPATH__'|" /home/vintagestory/server/server.sh

#no separate version file to update here anymore - status_agent.py now reads the running game's own
#/stats console output for its reported version, so it picks up the new version live once the game finishes
#starting back up, with no risk of going stale the way a static file did
sudo -u vintagestory /home/vintagestory/server/server.sh start
echo "Update complete: $VERSION"
'''

def updateGame(instanceId, gameName, dataPath):
    #see VS_UPDATE_SCRIPT above for why this isn't just "run server.sh update". Backgrounded (setsid) like
    #restartGame since downloading+extracting a new version can take a while and this SSM call should stay
    #fast regardless - but unlike restartGame, output goes to a log file (/tmp/vs-update.log) rather than
    #/dev/null, since this is a much more involved sequence with real failure points (network, disk, the
    #version API's shape) worth being able to actually diagnose after the fact.
    if gameName == 'vintagestory':
        script = VS_UPDATE_SCRIPT.replace('__DATAPATH__', dataPath)
        commands = [
            #quoted heredoc delimiter (<<'VSUPDATEEOF') is required here - without it bash would expand
            #$VERSION/${VERSION} etc. while WRITING the file, instead of leaving them for vs-update.sh's
            #own execution later. Same closing-terminator-on-its-own-line requirement noted in
            #VS_MOD_FIND_MODID_SCRIPT's caller applies here too.
            'cat > /tmp/vs-update.sh <<\'VSUPDATEEOF\'\n' + script + '\nVSUPDATEEOF\n',
            'chmod +x /tmp/vs-update.sh',
            'setsid /tmp/vs-update.sh </dev/null >/tmp/vs-update.log 2>&1 &',
        ]
    else:
        return "Update isn't supported for "+str(gameName)+" yet"
    print("updateGame: instanceId="+instanceId+" gameName="+gameName)
    success, stdout, stderr = runSsmCommandSync(instanceId, commands)
    print("updateGame result: success="+str(success)+" stdout="+repr(stdout)+" stderr="+repr(stderr))
    if success:
        return "Update requested - the server will be back in a few minutes once it's downloaded and restarted (check /tmp/vs-update.log on the instance if it doesn't come back)"
    return "Couldn't update the game: "+(stderr or stdout or "unknown error")[:300]

#docker-compose.yml's MODS value is a YAML block scalar (see valheim-prepare-data.sh in
#scripts/Valheim/install.sh) - its first line carries the "MODS=" prefix, every following line at the
#same indentation is a bare continuation entry. Both scripts below do one pass to collect every entry
#(regardless of which physical line carried the "MODS=" prefix), then rewrite the whole block so "MODS="
#always ends up back on the new first line - editing just the matched/inserted line in place would risk
#losing that prefix entirely if it happened to land on the line being touched.
VALHEIM_MOD_INSERT_SCRIPT = r'''python3 - <<'PYEOF'
path = {path}
modref = {modref}
with open(path) as f:
    lines = f.readlines()

out = []
i = 0
inserted = False
while i < len(lines):
    line = lines[i]
    stripped = line.strip()
    if stripped.startswith("MODS="):
        indent = line[:len(line) - len(line.lstrip())]
        entries = [stripped[len("MODS="):]]
        j = i + 1
        while j < len(lines):
            nxt = lines[j]
            nxt_stripped = nxt.strip()
            nxt_indent_len = len(nxt) - len(nxt.lstrip())
            if nxt_stripped == "" or nxt_indent_len < len(indent):
                break
            entries.append(nxt_stripped)
            j += 1
        entries.append(modref)
        out.append(indent + "MODS=" + entries[0] + "\n")
        for e in entries[1:]:
            out.append(indent + e + "\n")
        inserted = True
        i = j
        continue
    out.append(line)
    i += 1

if not inserted:
    print("NO_MODS_BLOCK")
    raise SystemExit(1)

with open(path, "w") as f:
    f.writelines(out)
print("INSERTED")
PYEOF'''

VALHEIM_MOD_DELETE_SCRIPT = r'''python3 - <<'PYEOF'
path = {path}
target = {modname}
with open(path) as f:
    lines = f.readlines()

out = []
i = 0
removed = False
while i < len(lines):
    line = lines[i]
    stripped = line.strip()
    if stripped.startswith("MODS="):
        indent = line[:len(line) - len(line.lstrip())]
        entries = [stripped[len("MODS="):]]
        j = i + 1
        while j < len(lines):
            nxt = lines[j]
            nxt_stripped = nxt.strip()
            nxt_indent_len = len(nxt) - len(nxt.lstrip())
            if nxt_stripped == "" or nxt_indent_len < len(indent):
                break
            entries.append(nxt_stripped)
            j += 1
        new_entries = []
        for e in entries:
            #Namespace-Name-Version, namespace/name never contain "-" per Thunderstore's own convention
            #(already relied on elsewhere this session)
            parts = e.split("-")
            name = parts[1] if len(parts) >= 3 else None
            if name == target and not removed:
                removed = True
                continue
            new_entries.append(e)
        if new_entries:
            out.append(indent + "MODS=" + new_entries[0] + "\n")
            for e in new_entries[1:]:
                out.append(indent + e + "\n")
        i = j
        continue
    out.append(line)
    i += 1

if not removed:
    print("NO_MATCH")
    raise SystemExit(1)

with open(path, "w") as f:
    f.writelines(out)
print("REMOVED")
PYEOF'''

def runSsmCommandSync(instanceId, commands):
    #shared by installMod/deleteMod - both are single synchronous SSM calls (unlike startWorldDownload's
    #async start/poll pair), since the fast part (console command / file edit) is expected to finish in a
    #couple seconds; the actual restart is always fired backgrounded within the command text itself so
    #this wait never blocks on it (see docs/frontend-ui-ux-requirements.md's timing discussion).
    #
    #A manual poll loop, not ssm.get_waiter('command_executed') - that waiter's documented signature also
    #wants a PluginName, which get_command_invocation itself has never needed here (getWorldDownloadStatus
    #calls it the same way, proven live repeatedly this session) - reusing that exact shape avoids the
    #open question of whether the waiter's PluginName is genuinely required. Bounded to fit comfortably
    #inside StartStopLambda's own Timeout (13s).
    response = ssm.send_command(
        InstanceIds=[instanceId],
        DocumentName='AWS-RunShellScript',
        Parameters={'commands': commands},
        TimeoutSeconds=60,
    )
    commandId = response['Command']['CommandId']
    for _ in range(10):
        try:
            result = ssm.get_command_invocation(CommandId=commandId, InstanceId=instanceId)
        except ssm.exceptions.InvocationDoesNotExist:
            time.sleep(1)
            continue
        if result['Status'] in ('Pending', 'InProgress', 'Delayed'):
            time.sleep(1)
            continue
        return result['Status'] == 'Success', result.get('StandardOutputContent', ''), result.get('StandardErrorContent', '')
    return False, '', 'Timed out waiting for the command to complete'

def updateDnsStateFunc(info):
    stepfunction = boto3.client('stepfunctions')
    consolidatedsmresponse = []
    for i in info['Instances']:
        hzi = i.get('hostedZoneId')
        dn = i.get('DomainName')
        inid = i.get('InstanceId')
        if dn != 'No domain tag found':
            response = stepfunction.start_execution(
                stateMachineArn=os.environ['stepfunctionarn'],
                input = "{\"hostedZoneId\": \""+hzi+"\",\"domainName\": \""+dn+"\",\"instanceId\": \""+inid+"\"}"
            )
            consolidatedsmresponse.append(response)
        else:
            consolidatedsmresponse.append("DNS update skipped for "+inid+" because no domain name found")
    return(consolidatedsmresponse)
