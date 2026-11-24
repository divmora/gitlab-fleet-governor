# AWS Lambda & Serverless Execution

GitLab Fleet Governor is engineered with dual runtime support. When deployed to AWS Lambda, it automatically detects the Lambda runtime environment and executes seamlessly without code changes.

---

## Runtime Auto-Detection

The application inspects the environment at startup:
- If `AWS_LAMBDA_FUNCTION_NAME` is set, `cmd/gitlab-fleet-governor/main.go` bypasses the CLI and invokes the AWS Lambda Go runtime adapter (`lambda.Start(handler.HandleRequest)`).
- If `AWS_LAMBDA_FUNCTION_NAME` is absent, standard Cobra CLI processing proceeds.

---

## Supported Event Triggers

The Lambda handler processes three trigger patterns:

```mermaid
flowchart LR
    EB[EventBridge Scheduled Rule<br/>e.g. rate 1 hour] --> Lambda[GitLab Fleet Governor<br/>AWS Lambda Handler]
    S3[S3 Put Object Event<br/>New policy.yaml uploaded] --> Lambda
    Direct[Direct Invocations / API Gateway<br/>JSON Payload] --> Lambda
    
    Lambda --> S3Cfg[Fetch S3 Policy / Secrets]
    Lambda --> GL[(GitLab Fleet API)]
```

### 1. Amazon EventBridge (CloudWatch Events) Scheduled Cron

Schedule periodic fleet audits (e.g. every hour):

```json
{
  "version": "0",
  "id": "53dc4d37-c2fc-431f-96b3-103dd95980e0",
  "detail-type": "Scheduled Event",
  "source": "aws.events",
  "account": "123456789012",
  "time": "2026-08-26T00:00:00Z",
  "region": "us-east-1",
  "resources": [
    "arn:aws:events:us-east-1:123456789012:rule/hourly-gitlab-governance"
  ],
  "detail": {}
}
```

Configuration is loaded from environment variables (`CONFIG_SOURCE=s3://my-corp-policies/governance.yaml`).

### 2. Amazon S3 Put Object Event

Automatically audit or enforce policies whenever a policy YAML/JSON file is uploaded to an S3 bucket:

```json
{
  "Records": [
    {
      "eventVersion": "2.1",
      "eventSource": "aws:s3",
      "awsRegion": "us-east-1",
      "eventTime": "2026-08-26T00:00:00.000Z",
      "eventName": "ObjectCreated:Put",
      "s3": {
        "s3SchemaVersion": "1.0",
        "configurationId": "PolicyUploadRule",
        "bucket": {
          "name": "my-corp-policies",
          "arn": "arn:aws:s3:::my-corp-policies"
        },
        "object": {
          "key": "production/governance.yaml",
          "size": 2048,
          "eTag": "b10a8db164e0754105b7a99be72e3fe5",
          "sequencer": "0A1B2C3D4E5F678901"
        }
      }
    }
  ]
}
```

### 3. Direct JSON Invocation Payload

Pass inline policy configurations or target overrides directly to the Lambda function:

```json
{
  "config_s3_uri": "s3://my-corp-policies/governance.yaml",
  "dry_run": true,
  "concurrency": 16,
  "log_level": "info",
  "group_paths_include": ["my-org/fintech"]
}
```

`config_s3_uri` must be an `s3://bucket/key` URI. Local paths, `file://` URIs and `-` (stdin) are rejected, so an invocation payload can't make the function read files from its own filesystem.

### Event-supplied configs are sanitised

A config that arrives with an event is treated as untrusted. That covers inline `config`, `config_content`, `config_yaml` or `config_json`, an object named by `config_s3_uri`, and the object in an S3 Put event. Such a config can't read the function's files, copy its secrets, or send its GitLab token to another host:

| Rule | Effect |
|---|---|
| **Limited `${VAR}` expansion** | Only `GITLAB_BASE_URL`, `CI_API_V4_URL` and the variables listed in `GLFG_EVENT_CONFIG_ALLOWED_ENV` (comma-separated) expand. Other references resolve as unset. Leave `settings.gitlab.token` and `settings.license.key` empty, and the function uses `GITLAB_TOKEN` and `DIVMORA_LICENSE_KEY` from its own environment. |
| **GitLab URL** | Without its own `settings.gitlab.token`, the config's `settings.gitlab.base_url` must be unset or match `GITLAB_BASE_URL`. A config that brings its own token may target any instance. |
| **No local files** | `repository_files[].content_file` must be an `s3://` URI (fetched when the config loads). `settings.license.file`, `crl_file` and `crl_url` are rejected. |

An event config may use an `instances` block (see [configuration](configuration.md#instances-reference)). Each instance then needs its own token in the config, because instance clients never fall back to `GITLAB_TOKEN` or `GITLAB_BASE_URL`; with the limited expansion above, that means a literal token or a variable listed in `GLFG_EVENT_CONFIG_ALLOWED_ENV`. Payload target overrides such as `group_paths_include` change the top-level `targets`, which only reach instances that don't set their own.

The config named by the function's own `CONFIG_SOURCE` / `CONFIG_FILE` / `CONFIG_CONTENT` environment variables is the operator's, so none of these rules apply to it. Neither do CLI, Docker or Kubernetes runs, which read the operator's own config file or ConfigMap.

---

## Standardized Lambda Response Envelope

All Lambda invocations return a structured response envelope:

```json
{
  "statusCode": 200,
  "status": "SUCCESS",
  "event_type": "EVENTBRIDGE_SCHEDULED",
  "config_source": "s3://my-corp-policies/governance.yaml",
  "dry_run": true,
  "summary": {
    "scanned_groups": 12,
    "matched_groups": 12,
    "scanned_projects": 145,
    "matched_projects": 145,
    "applied_operations": 0,
    "skipped_operations": 0,
    "failed_operations": 0
  },
  "metrics": {
    "total_scanned": 145,
    "total_targeted": 145,
    "total_changed": 18,
    "total_unchanged": 127,
    "total_failed": 0,
    "duration": 4250000000
  },
  "errors": [],
  "execution_duration_ms": 4250
}
```

---

## Local Lambda Emulation via CLI

You can test the Lambda handler locally using the `lambda` CLI subcommand:

```bash
gitlab-fleet-governor lambda --event examples/lambda-event.json
```

---

## AWS Lambda Container Image Deployment

GitLab Fleet Governor is packaged and published as an OCI-compliant AWS Lambda container image for both `x86_64` (amd64) and `arm64` architectures:

- **Registry**: `ghcr.io/divmora/gitlab-fleet-governor-lambda:latest` (and `:vX.Y.Z`)
- **Base Image**: `public.ecr.aws/lambda/provided:al2023`
- **Architectures**: `linux/amd64`, `linux/arm64`

### Deployment with AWS CLI

```bash
# Deploy to AWS Lambda using container image
aws lambda create-function \
  --function-name gitlab-fleet-governor \
  --package-type Image \
  --code ImageUri=ghcr.io/divmora/gitlab-fleet-governor-lambda:latest \
  --role arn:aws:iam::123456789012:role/gitlab-fleet-governor-execution-role \
  --timeout 300 \
  --memory-size 512 \
  --environment Variables="{GITLAB_TOKEN=glpat-xxxx,CONFIG_SOURCE=s3://my-corp-policies/governance.yaml}"
```

### Local Testing with Docker

```bash
# Run Lambda Container locally (starts Lambda Runtime Interface Emulator on :8080)
docker run -p 8080:8080 ghcr.io/divmora/gitlab-fleet-governor-lambda:latest

# Invoke locally via curl
curl -XPOST "http://localhost:8080/2015-03-31/functions/function/invocations" \
  -d @examples/lambda-event.json
```

