# Network & License Connectivity

GitLab Fleet Governor is self-hosted software. It runs entirely inside your environment (workstation, CI runner, Kubernetes, ECS or your own AWS Lambda), and DIVMORA operates no hosted service that your fleet data passes through.

This page lists every outbound connection the tool can make, what each one sends, and how to run it behind an egress proxy, through an internal mirror, or fully air-gapped.

---

## Outbound Connections

| Destination | When | Purpose | Required |
|---|---|---|---|
| Your GitLab instance (`settings.gitlab.base_url`) | Every run | Discovery, inspection and (when `dry_run: false`) mutation via the GitLab REST API | Yes |
| Your SMTP server | Only when the `audit` command emails a report | Report delivery | No |
| Your AWS S3 buckets | Only when you use `s3://` policy or template URIs | Loading policies and file templates | No |
| A CRL distribution point (default `https://crl.divmora.com/gitlab-fleet-governor.divcrl`) | Commercial licenses only, unless you supply a local CRL file (see below) | Checking whether your license has been revoked | No; can be mirrored or replaced with an offline file |

There is **no telemetry, usage reporting or phone-home**. The tool never sends fleet data, project names, policy contents, GitLab tokens, or your license key to DIVMORA.

---

## License Verification Works Offline

Commercial license keys are Ed25519-signed tokens. Expiry, the grace period, plan tier, project capacity and host/group scope are all checked **locally** from the signed token. No network access is needed for any of them.

The only license-related network call is the optional **Certificate Revocation List (CRL)** fetch. It answers one question: *has this license been revoked?* (for example after a refund, or a leaked key being replaced).

### What the CRL request sends

The CRL fetch is a plain HTTPS `GET` of a static, signed file:

- **Method & body:** `GET`, no request body.
- **Headers:** `User-Agent: divmora-license-go/crl-syncer`, plus standard caching headers (`If-None-Match` / `If-Modified-Since`) when a cached copy exists.
- **Not sent:** your license key or ID, customer name, host fingerprint, GitLab URL, or any fleet data.
- **Timeout:** 10 seconds. Responses are size-capped.

Every downloaded CRL must carry a valid DIVMORA signature and be issued for `gitlab-fleet-governor` (or all products). Anything else is rejected and never cached. This is why serving the CRL from your own mirror is safe: a mirror can relay the file, but it cannot forge or edit it.

### Replay and freshness protection

A mirror also cannot roll the CRL back or freeze it:

- **No rollback:** each CRL carries a signed, strictly increasing `sequence` number. A downloaded CRL older than the one already trusted (in memory or in the local cache) is rejected, and the newer cached copy is kept. A mirror therefore cannot serve an old list from before a license was revoked.
- **No indefinite staleness:** DIVMORA re-signs CRLs every 4 hours, each valid for 7 days (`next_update`). A downloaded or cached CRL more than 7 days past its `next_update` is no longer trusted. A mirror that stops refreshing is accepted for at most about 14 days after its last update.

Offline CRL files (`/etc/divmora/crl.divcrl`, `DIVMORA_CRL_FILE`) are exempt from the staleness rule, so air-gapped installs keep working between media imports.

---

## When a CRL Check Happens

The CRL source is resolved in this order (also documented in the [Configuration Guide](configuration.md#certificate-revocation-list-crl-resolution)):

1. **Local CRL (no network call):** `--license-crl`, `--license-crl-file`, `settings.license.crl` / `crl_file`, `DIVMORA_CRL` / `DIVMORA_CRL_FILE`, or the default file `/etc/divmora/crl.divcrl`.
2. **Explicit remote URL:** `--license-crl-url`, `settings.license.crl_url`, or `DIVMORA_CRL_URL`.
3. **Automatic:** with none of the above set, the tool fetches the CRL distribution point named in your license token. Commercial licenses issued by DIVMORA always name one (normally `https://crl.divmora.com/gitlab-fleet-governor.divcrl`, or a tenant-specific `https://crl.divmora.com/tenants/<org>.divcrl`), so **commercial installs make this check by default**. To avoid the outbound call, provide a local CRL (option 1) or a mirror URL (option 2).

Community Tier runs (no license key) never perform a CRL check.

### Behaviour when the CRL cannot be fetched

| Configuration | CRL endpoint unreachable |
|---|---|
| Local CRL file or inline CRL | Not applicable; no network call |
| Explicit URL (`--license-crl-url`, `settings.license.crl_url`, `DIVMORA_CRL_URL`) | Falls back to the last verified copy in the local cache, as long as it is no more than 7 days past its `next_update`. With **no usable cached copy**, license verification fails and the run stops. A mirror serving an older or stale CRL also fails the run. |
| Automatic (distribution point taken from the license token) | The run continues. Revocations from the newest CRL already cached still apply. |

> [!IMPORTANT]
> If you set an explicit CRL URL, make the cache file persistent so a temporary network or mirror outage doesn't stop governance runs. See [CRL cache](#crl-cache).

---

## Deployment Options

### 1. Direct egress

Allow outbound HTTPS to the CRL host only:

```
crl.divmora.com:443
```

No other DIVMORA endpoint is ever contacted.

### 2. Through your egress proxy

The CRL client honours the standard proxy environment variables:

```bash
export HTTPS_PROXY=http://proxy.corp.example:3128
export NO_PROXY=gitlab.corp.example
```

Set `NO_PROXY` for your GitLab host if GitLab traffic should not go through the proxy.

### 3. Through your own mirror

Host the CRL file on an internal endpoint (an artifact repository, an internal web server, or a reverse proxy in front of `crl.divmora.com`) and point the tool at it:

```bash
export DIVMORA_CRL_URL=https://artifacts.corp.example/divmora/gitlab-fleet-governor.divcrl
```

or in the policy file:

```yaml
settings:
  license:
    crl_url: "https://artifacts.corp.example/divmora/gitlab-fleet-governor.divcrl"
```

Refresh the mirror on a schedule (daily is recommended; it must be refreshed at least weekly, or explicit-URL runs start failing once the mirrored CRL is more than 7 days past its `next_update`) from the distribution point your license names: `https://crl.divmora.com/gitlab-fleet-governor.divcrl`, or your tenant-specific `https://crl.divmora.com/tenants/<org>.divcrl` if DIVMORA issued you a tenant-scoped license. The mirror needs no DIVMORA credentials. If it uses a certificate from a private CA, add that CA to the system trust store, or point `SSL_CERT_FILE` at a bundle that contains it.

### 4. Fully air-gapped

Download the signed CRL on a connected machine, transfer it with your normal media-import process, and place it where the tool reads it by default:

```bash
curl -fsSLo gitlab-fleet-governor.divcrl https://crl.divmora.com/gitlab-fleet-governor.divcrl
# transfer the file, then on the air-gapped host:
install -m 0644 gitlab-fleet-governor.divcrl /etc/divmora/crl.divcrl
```

Alternatively, set `DIVMORA_CRL_FILE` or `settings.license.crl_file` to any path. With a local CRL in place the tool makes **no outbound connections other than to your GitLab instance**.

On Kubernetes, store the CRL in a ConfigMap and mount it at `/etc/divmora/crl.divcrl`:

```bash
kubectl create configmap divmora-crl --from-file=crl.divcrl=gitlab-fleet-governor.divcrl
```

```yaml
# CronJob pod spec excerpt
volumeMounts:
  - name: divmora-crl
    mountPath: /etc/divmora/crl.divcrl
    subPath: crl.divcrl
    readOnly: true
volumes:
  - name: divmora-crl
    configMap:
      name: divmora-crl
```

---

## CRL Cache

When fetching from a URL, the last verified CRL is cached on disk and revalidated with ETag / `Last-Modified` on later runs. The cache location is resolved as:

1. `DIVMORA_CRL_CACHE_FILE`
2. `/etc/divmora/crl.cache`, if `/etc/divmora` exists and is writable
3. The OS temp directory (`divmora-crl.cache`)

Containers, CronJob pods and Lambda invocations usually start with an empty temp directory, so the cache does not survive between runs. For scheduled runs that use an explicit CRL URL, point `DIVMORA_CRL_CACHE_FILE` at a persistent volume (Kubernetes PVC, ECS EFS mount), or use a local CRL file instead.

---

## Security Review Summary

For vendor and security questionnaires:

- **Hosting model:** customer-hosted only; DIVMORA operates no SaaS for this product.
- **Data shared with DIVMORA:** none.
- **Outbound connections besides your own infrastructure:** at most one, an anonymous `GET` of a signed CRL file, which can be mirrored internally or replaced with an offline file.
- **Integrity:** CRLs and license keys are verified with DIVMORA's Ed25519 public key; tampered files are rejected, and older or stale CRLs cannot be replayed through a mirror.
- **Credentials:** GitLab tokens and license keys stay in your environment and are only ever sent to your own GitLab instance (tokens) or nowhere (license keys).
