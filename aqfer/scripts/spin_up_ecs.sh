#!/usr/bin/env bash
# set -e

aws cloudformation package --template-file /ecs_template_app_spec.yml --output-template-file /ecs_app_spec.yml --s3-bucket $1
aws cloudformation deploy --template-file /ecs_app_spec.yml --stack-name $2 --capabilities CAPABILITY_IAM CAPABILITY_NAMED_IAM --parameter-overrides \
  Id=$AWS_ACCESS_KEY_ID \
  Secret=$AWS_SECRET_ACCESS_KEY \
  Jwt=$Jwt \
  AWSAccount=$AWS_ACCOUNT \
  Ami=$Ami \
  Subnet=$Subnet \
  Subnet2=$Subnet2 \
  Vpc=$Vpc \
  AvailabilityZone=$AvailabilityZone \
  KeyPair=$KeyPair \
  LoadBalancerName=$LoadBalancerName \
  LBSecurityGroupName=$LBSecurityGroupName \
  TargetGroupName=$TargetGroupName \
  EC2InstanceType=$EC2InstanceType \
  EC2SecurityGroupName=${EC2SecurityGroupName} \
  EC2InstanceRoleName=${EC2InstanceRoleName} \
  ECSClusterName=$ECSClusterName \
  ECSServiceRoleName=$ECSServiceRoleName \
  ECSServiceName=$ECSServiceName \
  ECSTaskDefinitionName=$ECSTaskDefinitionName \
  ECRRepoURI=$ECRRepoURI \
  ContainerName=$ContainerName \
  AppLogGroupName=$AppLogGroupName \
  ECSLogGroupName=$ECSLogGroupName \

# Elasticache security group ingress from ec2 security group
aws ec2 authorize-security-group-ingress --group-name $3 --source-group $EC2SecurityGroupName --port $4 --protocol tcp
# Dax security group ingress from ec2 security group
# aws ec2 authorize-security-group-ingress --group-name $5 --source-group $EC2SecurityGroupName --port $6 --protocol tcp
