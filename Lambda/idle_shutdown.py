# Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
# SPDX-License-Identifier: MIT-0

import boto3
import json
import os


#Two consecutive empty checks stop a server, so the first one only records that it was empty. The record
#is an EC2 tag rather than anything held here, since this runs on a schedule with no memory of its own.
IDLE_CHECK_TAG = 'idle-check'

#Set on a server to keep it up regardless of player count, e.g. while working on it.
PAUSE_TAG = 'idle-shutdown'
PAUSE_VALUE = 'paused'


def lambda_handler(event, context):
    ec2 = boto3.client('ec2')
    s3 = boto3.client('s3')
    statusBucket = os.environ['statusBucket']
    managedTag = os.environ['managedTag']
    managedValue = os.environ['managedValue']

    response = ec2.describe_instances(Filters=[
        {'Name': 'tag:' + managedTag, 'Values': [managedValue]},
        {'Name': 'instance-state-name', 'Values': ['running']},
    ])

    for reservation in response['Reservations']:
        for instance in reservation['Instances']:
            instanceId = instance['InstanceId']
            tags = {t['Key']: t['Value'] for t in instance.get('Tags', [])}

            if tags.get(PAUSE_TAG) == PAUSE_VALUE:
                continue

            serverId = tags.get('server-id')
            if not serverId:
                print('Skipping ' + instanceId + ': no server-id tag')
                continue

            try:
                obj = s3.get_object(Bucket=statusBucket, Key='status/' + serverId + '.json')
                currentPlayers = json.loads(obj['Body'].read()).get('players', {}).get('current')
            except Exception:
                #No status reported yet, so whether anyone is playing can't be told. A server that never
                #reports is left running rather than stopped on a guess.
                continue

            wasIdle = tags.get(IDLE_CHECK_TAG) == 'true'
            #Covers both nobody playing and a game that isn't answering.
            isIdleNow = not currentPlayers

            if isIdleNow and wasIdle:
                ec2.stop_instances(InstanceIds=[instanceId])
                #Cleared on the stop itself, or a stopped server still reads "Idle - stopping soon".
                ec2.delete_tags(Resources=[instanceId], Tags=[{'Key': IDLE_CHECK_TAG}])
            elif isIdleNow:
                ec2.create_tags(Resources=[instanceId], Tags=[{'Key': IDLE_CHECK_TAG, 'Value': 'true'}])
            elif wasIdle:
                ec2.delete_tags(Resources=[instanceId], Tags=[{'Key': IDLE_CHECK_TAG}])
