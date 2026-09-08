# GitLab Fleet Governor Deployment Guide

This directory contains production-ready deployment configurations for orchestrating **GitLab Fleet Governor** across cloud-native environments:

| Target Platform | Technology Stack | Best Suited For | Location |
|---|---|---|---|
| **Kubernetes** | CronJob / Kustomize | Self-hosted Kubernetes, EKS, GKE, AKS clusters with GitOps (ArgoCD, Flux) | [`deploy/kubernetes`](./kubernetes/) |
| **AWS Lambda** | CloudFormation / Serverless | Cloud-native serverless periodic runs & S3 event triggers (fleets < 1,000 repos) | [`cloudformation-templates/gitlab-fleet-governor`](https://github.com/divmora/cloudformation-templates/tree/main/gitlab-fleet-governor) |
| **AWS ECS Fargate** | CloudFormation / Scheduled Tasks | Large enterprise fleets (> 1,000 to > 10,000 repos) with long runtimes (> 15 mins) | [`cloudformation-templates/gitlab-fleet-governor`](https://github.com/divmora/cloudformation-templates/tree/main/gitlab-fleet-governor) |

---

## Architectural Comparison Matrix

| Capability | Kubernetes CronJob | AWS Lambda | AWS ECS Fargate |
|---|---|---|---|
| **Max Execution Time** | Unlimited (configurable timeout) | 15 minutes (hard limit) | Unlimited (configurable timeout) |
| **Execution Trigger** | Cron (`batch/v1` schedule) | EventBridge cron, S3 Put, Direct JSON | EventBridge cron, CLI run-task |
| **Policy Source** | ConfigMap, Git volume, or S3 URI | S3 URI (`s3://bucket/key`) or JSON payload | S3 URI (`s3://bucket/key`) or mounted volume |
| **Secret Management** | Kubernetes Secret, Vault, External Secrets | AWS Secrets Manager, SSM Parameter Store | AWS Secrets Manager, SSM Parameter Store |
| **Scaling & Resource Limits** | Customizable CPU/Memory limits | 128 MB to 10 GB RAM | 0.25 to 16 vCPU, up to 120 GB RAM |
| **Network Isolation** | Pod Network / Calico / Cilium | AWS VPC Lambda ENI | Dedicated AWS VPC Subnets & Security Groups |

---

## Getting Started

- Deploy on Kubernetes: See [`deploy/kubernetes/README.md`](./kubernetes/README.md)
- Deploy on AWS Lambda (CloudFormation): See [`cloudformation-templates/gitlab-fleet-governor`](https://github.com/divmora/cloudformation-templates/tree/main/gitlab-fleet-governor)
- Deploy on AWS ECS Fargate (CloudFormation): See [`cloudformation-templates/gitlab-fleet-governor`](https://github.com/divmora/cloudformation-templates/tree/main/gitlab-fleet-governor)
