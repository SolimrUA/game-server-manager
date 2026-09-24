# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0


import boto3
import json
import os
import shlex
import time


def lambda_handler(event, context):
    
    tagKey = event['tagName']
    tagValue = event['tagValue']
    targetInstanceId = event.get('instanceId') #restricts the action to one instance instead of all tagged ones
    global ec2
    global s3
    instanceIds = []
    info = []
    serverResizeCheck = "OK"
    statemachineresponse = {}

    ec2 = boto3.client('ec2')
    s3 = boto3.client('s3')
    global backup
    backup = boto3.client('backup')
    global ssm
    ssm = boto3.client('ssm')
    global events
    events = boto3.client('events')
    info = getInfo(tagKey, tagValue)
    
    if len(info['Instances']) < 1:
        statusmessage = "No gaming server instances found"
        return(statusmessage)

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

    #API Gateway's VTL template renders the multi-valued cognito:groups claim as the string
    #"[admins, users]" rather than JSON, so strip brackets instead of assuming a clean comma list.
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
            return ("You don't have permission to perform this action", info)

    if event['command'] == "start":
        try:
            ec2.start_instances(InstanceIds=instanceIds)
            statusmessage = "If this message appears, something has gone very wrong"
        except:
            print("start failed")
            statusmessage = "Couldn't start server, please try again later"
            return(statusmessage,info)
        #idle-check is an EC2 tag, so it outlives a stop/start cycle: left in place, the first idle
        #check after this start counts as the second consecutive empty one and stops the instance.
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
        #Clear idle-check on stop too, so an already-stopped instance never reads "Idle - stopping soon".
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
        #Returns a job handle rather than the usual [statusmessage, info] tuple, which the front end
        #polls getWorldDownloadStatus with.
        if not targetInstances:
            return {"status": "Failed", "error": "No matching instance found"}
        i = targetInstances[0]
        gameName = i.get('GameName')
        dataPath = i.get('DataPath')
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
        #s3Key is client-supplied: without this, a caller with download rights on any one game could
        #pass another game's s3Key and get a presigned URL to a world they can't download.
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
        #Vintage Story's moddb needs the server's game version as an explicit argument - left off, the
        #server looks "current" up itself through a request that fails inside its own moddb client.
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
        #The update restarts the game itself, so no restartGame call is needed after it.
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
                #Read from the instance's tags, never trusted from the client
                infoDict['GameName'] = gameName
                infoDict['DataPath'] = next((i.get('Value') for i in instance['Tags'] if i.get('Key') == 'data-path'), None)
                infoDict['WorldDownloadPaths'] = next((i.get('Value') for i in instance['Tags'] if i.get('Key') == 'world-download-paths'), None)
                infoDict['WorldDownloadCorePaths'] = next((i.get('Value') for i in instance['Tags'] if i.get('Key') == 'world-download-core-paths'), None)
                infoDict['GameStatus'] = getGameStatus(gameName) if infoDict['State'] == 'running' else None
                #Promoted out of GameStatus: the front end's Server Name column reads it top-level.
                infoDict['ServerName'] = infoDict['GameStatus'].get('serverName') if infoDict['GameStatus'] else None
                infoDict['LastBackupTime'] = getLastBackupTime(gameName)
                infoDict['IdleShutdownStatus'] = getIdleShutdownStatus(gameName, instance.get('Tags', []))
                #cfn/server-stack.yaml's AllowUserLifecycle/AllowUserDownloads, as tags
                infoDict['UserLifecycleAllowed'] = next((i.get('Value') == 'true' for i in instance['Tags'] if i.get('Key') == 'user-lifecycle-allowed'), False)
                infoDict['UserDownloadsAllowed'] = next((i.get('Value') == 'true' for i in instance['Tags'] if i.get('Key') == 'user-downloads-allowed'), False)
                info["Instances"].append(infoDict)
    return(info)

def getGameStatus(gameName):
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
    #cfn/server-stack.yaml names vaults by a fixed convention, so this needs no lookup.
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
    #"triggered-once" means one empty check is already recorded, so the next one stops the instance.
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
    #The role name has to be derived from a convention rather than looked up: the Resource Groups
    #Tagging API doesn't index IAM roles at all.
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
    #Only the folders includePaths names, never the whole data directory: Vintage Story's
    #serverconfig.json holds the join password. Requires the instance to be running.
    #instanceId precedes scope in the key so getWorldDownloadStatus's ownership check is
    #scope-independent.
    bucket = os.environ.get('statusBucket')
    key = 'worlds/'+gameName+'/'+instanceId+'-'+scope+'-'+str(int(time.time()))+'.zip'
    folders = ' '.join('"'+p.strip()+'"' for p in includePaths.split(','))
    commands = [
        #A brand new world may not have every folder yet; `zip -r ... f1 f2 f3` would fail on the lot.
        'cd '+dataPath+' && for p in '+folders+'; do [ -e "$p" ] && zip -r /tmp/worldsave.zip "$p"; done',
        'aws s3 cp /tmp/worldsave.zip s3://'+bucket+'/'+key,
        'rm -f /tmp/worldsave.zip',
    ]
    #SSM defaults to 3600s, too short for a multi-GB world (its own max is 172800).
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
        #Briefly expected right after send_command, before the command registers on the instance.
        return {"status": "InProgress"}
    status = result['Status']
    if status in ('Pending', 'InProgress', 'Delayed'):
        return {"status": "InProgress"}
    if status == 'Success':
        #Signing is local and succeeds whatever the signer's permissions are - the s3:GetObject grant on
        #this Lambda's role is what makes the URL work when the browser follows it.
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

#Mirrors status_agent.py's MODS_PATH.
VS_MODS_SUBDIR = "Mods"

#A mod's display name (all the caller has) differs from the modid /moddb remove needs - e.g.
#"Carry Capacity" vs "carrycapacity". Reads modinfo.json the same way status_agent.py's read_mods()
#does, so it resolves the same entry the front end is showing.
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
    #Deliberately doesn't restart the game: an admin installing several mods in a row shouldn't pay a
    #restart each time, and calls restartGame once when ready.
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
            #Odin silently ignores MODS unless TYPE=BepInEx is set. Fail loudly rather than leave the
            #file edited and the mod never loaded; enabling BepInEx is not done automatically.
            'grep -q "^ *- TYPE=BepInEx$" ' + shlex.quote(composeFile) + ' || { echo "BepInEx is not enabled on this server - set TYPE=BepInEx in docker-compose.yml first" >&2; exit 1; }',
            script,
        ]
    else:
        return "Mod install isn't supported for "+str(gameName)
    print("installMod: instanceId="+instanceId+" gameName="+gameName+" modref="+modref)
    success, stdout, stderr = runSsmCommandSync(instanceId, commands)
    #Logged either way: injecting a console command always exits 0, whether or not the game acted on
    #it, so this output is the only evidence of what the game printed back.
    print("installMod result: success="+str(success)+" stdout="+repr(stdout)+" stderr="+repr(stderr))
    if success:
        return "Mod install requested - restart the server (once you're ready) to load it"
    return "Couldn't install mod: "+(stderr or stdout or "unknown error")[:300]

def deleteMod(instanceId, gameName, dataPath, modname):
    #Doesn't restart the game, for the same reason installMod doesn't.
    if gameName == 'vintagestory':
        #/moddb remove takes a modid, so the caller's display name has to be resolved to one first.
        findScript = VS_MOD_FIND_MODID_SCRIPT.format(modsPath=repr(dataPath+'/'+VS_MODS_SUBDIR), modname=repr(modname))
        commands = [
            #The "\n" is load bearing: bash needs the closing PYEOF alone on its line, or it finds no
            #terminator and reads to end-of-script.
            'MODID=$(' + findScript + '\n)',
            #$(...) swallows the find script's "NO_MATCH" into MODID instead of stdout - re-echo it so
            #the caller's check for it still sees it.
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
    #Backgrounded (setsid, redirected fds) because a restart takes a while - Valheim's graceful-stop
    #period alone is 60s - and this SSM call has to return inside the Lambda's timeout.
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

#server.sh's own `update` subcommand is a disabled stub that prints manual instructions and exits 0
#("Auto update is disabled because unreliable"), so the sequence is reimplemented here.
VS_UPDATE_SCRIPT = r'''#!/bin/bash
set -e

#The API lists pre-release tags like "1.4.4-dev.2" alongside stable ones.
VERSION=$(curl -fsS https://mods.vintagestory.at/api/gameversions | jq -r '[.gameversions[].name | select(contains("-") | not)] | last')
if [ -z "$VERSION" ] || [ "$VERSION" = "null" ]; then
  echo "Couldn't determine the latest available version" >&2
  exit 1
fi
echo "Updating Vintage Story server to $VERSION"

sudo -u vintagestory /home/vintagestory/server/server.sh stop

#Extracting over an existing install mixes old and new asset files, and the game then refuses to start
#("Your Server installation still contains old files from a previous game version"). Safe to wipe:
#this directory only ever holds the game binary, and DataPath is a separate top-level directory.
sudo rm -rf /home/vintagestory/server/*

cd /tmp
sudo wget -O vs_server_update.tar.gz "https://cdn.vintagestory.at/gamefiles/stable/vs_server_linux-x64_${VERSION}.tar.gz"
sudo tar -C /home/vintagestory/server -xzf vs_server_update.tar.gz
#The tarball is root-owned (sudo wget), so a plain rm -f leaves it behind with "Operation not permitted".
sudo rm -f vs_server_update.tar.gz

#server/ only, never all of /home/vintagestory: an update must not touch the world save.
sudo chown -R vintagestory:vintagestory /home/vintagestory/server
sudo chmod +x /home/vintagestory/server/VintagestoryServer /home/vintagestory/server/server.sh

#The extracted server.sh ships the stock DATAPATH - repatch it, or the server silently comes back up
#pointed at the wrong data directory.
sudo sed -i "s|^DATAPATH='/var/vintagestory/data'|DATAPATH='__DATAPATH__'|" /home/vintagestory/server/server.sh

sudo -u vintagestory /home/vintagestory/server/server.sh start
echo "Update complete: $VERSION"
'''

def updateGame(instanceId, gameName, dataPath):
    #Backgrounded (setsid) like restartGame, since downloading and extracting a release takes minutes.
    #Logged to a file rather than /dev/null so a failure partway through is diagnosable.
    if gameName == 'vintagestory':
        script = VS_UPDATE_SCRIPT.replace('__DATAPATH__', dataPath)
        commands = [
            #The quoted delimiter is required: unquoted, bash expands $VERSION while writing the file
            #instead of leaving it for vs-update.sh to expand when it runs.
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

#MODS is a YAML block scalar: its first line carries the "MODS=" prefix, every following line at that
#indentation is a bare entry. Both scripts below rewrite the whole block, since editing one line in
#place would drop the prefix whenever that line was the first one.
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
            #Namespace-Name-Version; Thunderstore namespaces and names never contain "-" themselves.
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
    #Safe to wait on, unlike startWorldDownload: a console command or file edit finishes in seconds,
    #and anything slow is backgrounded inside the command text itself. A manual loop rather than
    #ssm.get_waiter('command_executed'), whose signature also wants a PluginName this doesn't have.
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
