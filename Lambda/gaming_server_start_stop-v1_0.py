# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0


import boto3
import json
import os
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
        includePaths = i.get('WorldDownloadPaths')
        if not gameName or not dataPath or not includePaths:
            return {"status": "Failed", "error": "Instance is missing its game-name/data-path/world-download-paths tag"}
        try:
            return startWorldDownload(i['InstanceId'], gameName, dataPath, includePaths)
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
                infoDict['GameStatus'] = getGameStatus(gameName) if infoDict['State'] == 'running' else None
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

def startWorldDownload(instanceId, gameName, dataPath, includePaths):
    #Zips specific subfolders of the currently-live, currently-mounted data directory (not the whole
    #thing - e.g. Vintage Story's serverconfig.json has a Password field in it that has no business in a
    #downloadable file) via SSM Run Command and uploads the result to the shared status bucket under a
    #worlds/ prefix. Only works while the instance is running. includePaths is a comma-separated list of
    #paths relative to dataPath (the world-download-paths tag, set per-game in cfn/server-stack.yaml) -
    #kept driven by a tag rather than hardcoded here so this stays game-agnostic. The S3 key is built up
    #front, not parsed out of the command's own output later, which would be fragile.
    bucket = os.environ.get('statusBucket')
    key = 'worlds/'+gameName+'/'+instanceId+'-'+str(int(time.time()))+'.zip'
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
        url = s3.generate_presigned_url('get_object', Params={'Bucket': bucket, 'Key': s3Key}, ExpiresIn=900)
        return {"status": "Success", "url": url}
    return {"status": "Failed", "error": result.get('StandardErrorContent', '')[:500]}

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
