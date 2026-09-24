# Changelog

### Breaking Changes

* **license:** `fleet-license-gen` administrative utility has been deprecated and removed. Operators and administrators should install the official centralized multi-product license CLI:
  ```bash
  go install github.com/divmora/license-go/cmd/license-cli@v1.0.0
  ```

## [0.11.0](https://github.com/divmora/gitlab-fleet-governor/compare/v0.10.0...v0.11.0) (2026-09-24)


### Features

* **onboarding:** add `export` command for reverse-sync of live fleet state to policy.yaml ([#60](https://github.com/divmora/gitlab-fleet-governor/issues/60)) ([bb91d06](https://github.com/divmora/gitlab-fleet-governor/commit/bb91d063aae99716dcfa6f815425e96c1fbf763b))

## [0.10.0](https://github.com/divmora/gitlab-fleet-governor/compare/v0.9.0...v0.10.0) (2026-09-24)


### Features

* **governance:** support merge request branch workflow (target branch rules) ([#48](https://github.com/divmora/gitlab-fleet-governor/issues/48)) ([28a8805](https://github.com/divmora/gitlab-fleet-governor/commit/28a8805f8a3c42f53cc609d3e45ee4cf383f1965))

## [0.9.0](https://github.com/divmora/gitlab-fleet-governor/compare/v0.8.0...v0.9.0) (2026-09-22)


### Features

* **license:** upgrade to license-go v1.3.0 and support offline/remote CRL revocation ([#44](https://github.com/divmora/gitlab-fleet-governor/issues/44)) ([02344dc](https://github.com/divmora/gitlab-fleet-governor/commit/02344dcd830a35879373107f088c227ddca423fb))
* **license:** upgrade to license-go v1.3.1 ([#47](https://github.com/divmora/gitlab-fleet-governor/issues/47)) ([0aa9f50](https://github.com/divmora/gitlab-fleet-governor/commit/0aa9f50f9411d7e154045cfda654a05d969bbb58))

## [0.8.0](https://github.com/divmora/gitlab-fleet-governor/compare/v0.7.0...v0.8.0) (2026-09-19)


### ⚠ BREAKING CHANGES

* **license:** cmd/fleet-license-gen has been removed. Operators should install github.com/divmora/license-go/cmd/license-cli@v1.0.0.

### Features

* **discovery:** handle projects marked for deletion in discovery, audit, and engine ([17a214d](https://github.com/divmora/gitlab-fleet-governor/commit/17a214dfdf493c9e3185898b925932da543c762e))
* **license:** adopt pro and enterprise tier-to-features matrix resolution ([020782b](https://github.com/divmora/gitlab-fleet-governor/commit/020782b3ae9812fed352817faadc731978ed3b02))
* **license:** modernize provenance evaluation, adopt standardized status cards, and deprecate fleet-license-gen ([89db5ca](https://github.com/divmora/gitlab-fleet-governor/commit/89db5ca3a5b3337e7c663a7b63456b49d44349fb))
* **license:** unify commercial licensing under DIVMORA_LICENSE_KEY with multi-product claim scoping ([4016010](https://github.com/divmora/gitlab-fleet-governor/commit/40160107b3041c6af04a11b6ab926bf032f115f7))
* **license:** upgrade to license-go v0.5.0 and adopt upstream keyring, scoping, and host primitives ([d2d38a7](https://github.com/divmora/gitlab-fleet-governor/commit/d2d38a769aa48885e46b4c31fc51fd855ba75d7f))
* **license:** upgrade to license-go v0.6.0 and adopt release attestation, declarative BSL grants, and status formatters ([d43c4f8](https://github.com/divmora/gitlab-fleet-governor/commit/d43c4f8e6682811475c9b5a81d58514c017a26b1))
* **license:** upgrade to license-go v0.7.0 and adopt native placeholder attestation ([6b98fa6](https://github.com/divmora/gitlab-fleet-governor/commit/6b98fa611401f99d5ca51125daae3fc2441cfe65))
* **license:** upgrade to license-go v1.0.0 and standardize runtime verification without regressions ([#39](https://github.com/divmora/gitlab-fleet-governor/issues/39)) ([df56f54](https://github.com/divmora/gitlab-fleet-governor/commit/df56f54a65ec80c83ffe20a777ea47db83ddd13c))
* **license:** upgrade to license-go v1.1.0 ([30dd8a3](https://github.com/divmora/gitlab-fleet-governor/commit/30dd8a3a93eb5b1a0b96a345fe634ddd16a7e3ef))


### Bug Fixes

* **docker:** correct entrypoint CMD to ["bootstrap"] and copy only to task root ([#38](https://github.com/divmora/gitlab-fleet-governor/issues/38)) ([e56d732](https://github.com/divmora/gitlab-fleet-governor/commit/e56d732c479c32e481d63c8390682fd9ec3a4922))


### Miscellaneous Chores

* **license:** deprecate and remove legacy cmd/fleet-license-gen ([#40](https://github.com/divmora/gitlab-fleet-governor/issues/40)) ([59cd745](https://github.com/divmora/gitlab-fleet-governor/commit/59cd745313b04233592f1a6fe592c2d2b50234ca))

## [0.7.0](https://github.com/divmora/gitlab-fleet-governor/compare/v0.6.1...v0.7.0) (2026-09-11)


### Features

* **ci:** add workflow_dispatch to release-please workflow for manual releases ([76174f3](https://github.com/divmora/gitlab-fleet-governor/commit/76174f37717cf3dfa78707e5308dd01e1fb9b785))

## [0.6.1](https://github.com/divmora/gitlab-fleet-governor/compare/v0.6.0...v0.6.1) (2026-09-11)


### Bug Fixes

* **audit:** preserve commercial license in dry-run mode and include governor version in reports ([eceb650](https://github.com/divmora/gitlab-fleet-governor/commit/eceb6504ce5924be5d72b5b700fa4c698e343c3d))

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
