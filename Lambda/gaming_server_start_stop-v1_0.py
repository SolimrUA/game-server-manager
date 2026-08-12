# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0


import boto3
import json
import os


def lambda_handler(event, context): #standard function called on lambda invocation
    
    tagKey = event['tagName'] #This is the Tag for the resources we're looking to handle
    tagValue = event['tagValue'] #This is the Tag for the resources we're looking to handle
    targetInstanceId = event.get('instanceId') #Optional - restricts start/stop/resize to a single instance instead of all tagged instances
    global ec2
    instanceIds = [] 
    info = []
    serverResizeCheck = "OK"
    statemachineresponse = {}
    
    ec2 = boto3.client('ec2') #Sets up ec2 as the object to call the boto3 (AWS Python SDK) client library for the EC2 service
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
        

    if event['command'] == "start":
        try:
            ec2.start_instances(InstanceIds=instanceIds)
            statusmessage = "If this message appears, something has gone very wrong"
        except:
            print("start failed")
            statusmessage = "Couldn't start server, please try again later"
            return(statusmessage,info)
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
                info["Instances"].append(infoDict)
    return(info)
    
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
