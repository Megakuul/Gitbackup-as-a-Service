# Gitbackup as a Service

This is a AWS integrated application to backup Github repositorys.



### Deployment on AWS

The infrastructure is deployed using AWS CloudFormation. For initial deployment you can use the AWS Cli:

**Important**: Because CloudFormation suffers a painful chicken-egg problem with Lambda code, you must first create a dummy bucket, this bucket can be deleted when the first CloudFormation Stack is running.

```bash
# Compile function
cd function
CGO_ENABLED=0 GOOS=linux go build -o ../main main.go
cd ..
zip backupfn.zip main

# Create bucket and upload function
aws s3 mb s3://initbucket-gbaas-prod-eu-central-1-1
aws s3 cp backupfn.zip s3://initbucket-gbaas-prod-eu-central-1-1

# Create cloudformation stack with the init bucket
aws cloudformation create-stack --stack-name stack-gbaas-prod-eu-central-1-1 \
--template-body file://./deploy.yaml --capabilities CAPABILITY_IAM \
--parameters ParameterKey=UseDefBucket,ParameterValue=false \
ParameterKey=BackupFunctionBucket,ParameterValue=initbucket-gbaas-prod-eu-central-1-1

# Wait for the stack to be initialized
aws cloudformation wait stack-create-complete --stack-name stack-gbaas-prod-eu-central-1-1 

# Now upload the function to the real bucket
aws s3 cp backupfn.zip s3://corebucket-gbaas-prod-eu-central-1-1
# Update the lambda function
aws lambda update-function-code --function-name backupfn-gbaas-prod-eu-central-1-1 \
--s3-bucket s3://corebucket-gbaas-prod-eu-central-1-1 \
--s3-key backupfn.zip

# If this is done you can now delete the dummy bucket
aws s3 rm s3://initbucket-gbaas-prod-eu-central-1-1 --recursive
aws s3 rb s3://initbucket-gbaas-prod-eu-central-1-1
```

Once set up with that, the Action workflow can handle everything (as long as this backupfn.zip exists in the corebucket).

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
CGO_ENABLED=0 GOOS=linux go build -o main function/main.go
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
