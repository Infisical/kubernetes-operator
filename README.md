<p align="center">
  <p align="center"><b>Infisical Kubernetes Operator </b>
</p>
<h4 align="center">
  |
  <a href="https://infisical.com/docs/integrations/platforms/kubernetes/overview">Documentation</a> |
  <a href="https://www.infisical.com">Website</a> | 
  <a href="https://infisical.com/slack">Slack</a>
  |
</h4>

<h4 align="center">
  <a href="https://github.com/Infisical/kubernetes-operator/blob/main/LICENSE">
    <img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="Infisical is released under the MIT license." />
  </a>
  <a href="https://github.com/infisical/infisical/blob/main/CONTRIBUTING.md">
    <img src="https://img.shields.io/badge/PRs-Welcome-brightgreen" alt="PRs welcome!" />
  </a>
  <a href="https://github.com/Infisical/infisical/issues">
    <img src="https://img.shields.io/github/commit-activity/m/infisical/infisical" alt="git commit activity" />
  </a>
  <a href="https://infisical.com/slack">
    <img src="https://img.shields.io/badge/chat-on%20Slack-blueviolet" alt="Slack community channel" />
  </a>
  <a href="https://twitter.com/infisical">
    <img src="https://img.shields.io/twitter/follow/infisical?label=Follow" alt="Infisical Twitter" />
  </a>
</h4>

## Introduction

**[Infisical](https://infisical.com)** is the open source secret management platform that teams use to centralize their secrets like API keys, database credentials, and configurations.

The Infisical Operator is a collection of Kubernetes controllers that streamline how secrets are managed between Infisical and your Kubernetes cluster. It provides multiple Custom Resource Definitions (CRDs) which enable you to:

- Sync secrets from Infisical into Kubernetes (`InfisicalSecret`).
- Push new secrets from Kubernetes to Infisical (`InfisicalPushSecret`).
- Manage dynamic secrets and automatically create time-bound leases (`InfisicalDynamicSecret`).

## Metrics

The operator serves Prometheus metrics over HTTPS on port `8443` (set with `--metrics-bind-address`). If you run the Prometheus Operator, set `telemetry.serviceMonitor.enabled=true` in the Helm chart and it creates a `ServiceMonitor` that scrapes the endpoint.

Besides the standard `controller_runtime_*` and `workqueue_*` metrics, the operator exposes the following metrics about `InfisicalStaticSecret` resources:

| Metric | Type | Labels | Description |
|---|---|---|---|
| `infisical_static_secrets` | gauge | `namespace` | Number of `InfisicalStaticSecret` resources |
| `infisical_static_secrets_sync_failed` | gauge | `namespace` | Number of `InfisicalStaticSecret` resources whose last reconciliation failed |
| `infisical_static_secret_sync_status` | gauge | `namespace`, `name`, `status` | `1` for the current status of the last reconciliation (`synced`, `failed`, or `unknown` before the first one), `0` for the others |
| `infisical_static_secret_refresh_interval_seconds` | gauge | `namespace`, `name` | Configured `refreshInterval` in seconds |
| `infisical_static_secret_info` | gauge | `namespace`, `name`, `auth_namespace`, `auth_name`, `instant_updates` | Configuration of the resource, always `1` |
| `infisical_static_secret_workload_redeploys_total` | counter | `namespace`, `name` | Workloads redeployed through auto-reload because a target of the resource changed |

Example queries:

```promql
# Resources synced from Infisical, over time
sum(infisical_static_secrets)

# Alert on a resource that has been failing to sync (add "for: 10m" to the rule)
infisical_static_secret_sync_status{status="failed"} == 1

# Average refresh interval
avg(infisical_static_secret_refresh_interval_seconds)

# Resources using instant updates
count(infisical_static_secret_info{instant_updates="true"})

# Auto-reload redeploys in the last 24 hours
sum(increase(infisical_static_secret_workload_redeploys_total[24h]))
```

If you run more than one replica, every replica reports the same gauges, so aggregate them with `max` rather than `sum`.

## Security

Please do not file GitHub issues or post on our public forum for security vulnerabilities, as they are public!

Infisical takes security issues very seriously. If you have any concerns about Infisical or believe you have uncovered a vulnerability, please get in touch via the e-mail address security@infisical.com. In the message, try to provide a description of the issue and ideally a way of reproducing it. The security team will get back to you as soon as possible.

Note that this security address should be used only for undisclosed vulnerabilities. Please report any security problems to us before disclosing it publicly.
