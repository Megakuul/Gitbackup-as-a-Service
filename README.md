# Gitbackup as a Service

This is a AWS integrated application to backup Github repositorys.



### Deployment on AWS

The infrastructure is deployed using AWS CloudFormation. For initial deployment you can use the AWS Cli:

```bash
aws cloudformation create-stack --stack-name stack-gbaas-prod-eu-central-1-1 --template-body file://./deploy.yaml --capabilities CAPABILITY_IAM
```

*Note* building of the CloudFormation Stack requires a AWS user with the following policies attached:
- AWSCloudFormationFullAccess
- AmazonS3FullAccess
- AWSLambda_FullAccess
- AmazonEventBridgeFullAccess
- CloudFrontFullAccess
- IAMFullAccess

(IAM permissions can also be assigned more granularly, it must only be possible to create IAM roles)

### Update System

**Important** updating the system requires the CloudFormation Stack and its ressources to exist in a valid state.

To update the system you can either use the following commands:

```bash
# Update CloudFormation stack
aws cloudformation update-stack --stack-name stack-gbaas-prod-eu-central-1-1 \
--template-body file://./deploy.yaml --capabilities CAPABILITY_IAM

# Output corebucket_name
aws cloudformation describe-stacks --stack-name stack-gbaas-prod-eu-central-1-1 \
--query "Stacks[0].Outputs[0].OutputValue"
# Output backupfn_name
aws cloudformation describe-stacks --stack-name stack-gbaas-prod-eu-central-1-1 \
--query "Stacks[0].Outputs[1].OutputValue"

# Compile function
GOOS=linux go build -o main function/main.go
zip backupfn.zip main

# Upload function and webapp (corebucket_name is the first output of cloudformation)
aws s3 cp backupfn.zip s3://<corebucket_name>/backupfn.zip
aws s3 cp web s3://<corebucket_name>/web --recursive

# Update lambda code (backupfn_name is the second output of cloudformation)
aws lambda update-function-code --function-name <backupfn_name> \
--s3-bucket <corebucket_name> \
--s3-key backupfn.zip
```

Or alternatively the provided Github Workflow can be used to update the system on changes at the code.

The Action requires the following variables in your Repository:

Repository Secrets:
- *AWS_ACCESS_KEY*: youraccesskey
- *AWS_ACCESS_SECRET*: yoursecretkey

Repository Variables:
- *AWS_DEFAULT_REGION*: eu-central-1


The Workflow will do the following steps:
- Checks whether the stack exists and is in a valid state, and continues if this is the case.
- Applies the CloudFormation Stack
- Compiles and uploads the Backup Function to the Core Bucket
- Updates the Lambda function with the new code
- Uploads the Webapp to the Core Bucket
