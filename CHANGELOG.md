# Changelog

## [0.6.0](https://github.com/divmora/gitlab-fleet-governor/compare/v0.5.0...v0.6.0) (2026-09-11)


### Features

* **license:** implement BSL 1.1 commercial licensing and 4-layer defense architecture ([#33](https://github.com/divmora/gitlab-fleet-governor/issues/33)) ([ab1af8a](https://github.com/divmora/gitlab-fleet-governor/commit/ab1af8ad29561cabea9c68c744c34151f41fac2b))

## [0.5.0](https://github.com/divmora/gitlab-fleet-governor/compare/v0.4.0...v0.5.0) (2026-09-11)


### Features

* **audit:** add CC and BCC recipient support to SMTP audit distribution ([a86299a](https://github.com/divmora/gitlab-fleet-governor/commit/a86299afa3dca5cc07b5f556324ba9c1310b8e89))

## [0.4.0](https://github.com/divmora/gitlab-fleet-governor/compare/v0.3.0...v0.4.0) (2026-09-09)


### Features

* add configurable greeting to audit SMTP email report ([67893b3](https://github.com/divmora/gitlab-fleet-governor/commit/67893b3777aadc4eca174d5a1446d6a46480d04f))
* **audit:** add pipeline retention and unpruned pipeline audit module ([375892d](https://github.com/divmora/gitlab-fleet-governor/commit/375892d09784e858f3590d80a69be39e5cd57c7b))

## [0.3.0](https://github.com/divmora/gitlab-fleet-governor/compare/v0.2.0...v0.3.0) (2026-09-08)


### Features

* **audit:** add fleet-wide compliance & security audit command with multi-sheet excel and smtp dispatch ([ec3dde3](https://github.com/divmora/gitlab-fleet-governor/commit/ec3dde3220dd51cc1d8eaa08a8fc85bf9b8296de))
* **audit:** include authenticated token user identity in executive summary and audit reports ([e92914c](https://github.com/divmora/gitlab-fleet-governor/commit/e92914ce8b0fa6374e9906ad292fdb3077da80df))
* **audit:** make service account and bot identification fully configurable by username, custom email, and pattern ([68b0fa9](https://github.com/divmora/gitlab-fleet-governor/commit/68b0fa99f5d71d839c33c3d411dbe2b2220e9660))
* **audit:** recognize GitLab Enterprise service account emails (service_account_*[@noreply](https://github.com/noreply).*) ([6db69b7](https://github.com/divmora/gitlab-fleet-governor/commit/6db69b72a35f379e9dba3049b84498cba348c59e))
* **audit:** separate bot findings, de-prioritize archived projects, resolve user IDs, and add remediation guidelines ([64b1a65](https://github.com/divmora/gitlab-fleet-governor/commit/64b1a65f05bc8211e21090e7e84082bc1158f1a2))
* **deploy:** add kubernetes cronjob manifests and deployment guide ([4e0d61a](https://github.com/divmora/gitlab-fleet-governor/commit/4e0d61a4a829c40d0a634f0e1b822ec9964a1290))


### Bug Fixes

* **audit:** resolve golangci-lint ineffectual assignment and staticcheck cutset warnings ([5e3117b](https://github.com/divmora/gitlab-fleet-governor/commit/5e3117be6bb67c34e8ae0931a1515f5154716408))

## [0.2.0](https://github.com/divmora/gitlab-fleet-governor/compare/v0.1.0...v0.2.0) (2026-09-05)


### Features

* introduce gitlab fleet governor ([d104d9e](https://github.com/divmora/gitlab-fleet-governor/commit/d104d9e3771a1d755fb052da68e4d42ce6d673f4))
