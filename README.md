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

## Security

Please do not file GitHub issues or post on our public forum for security vulnerabilities, as they are public!

Infisical takes security issues very seriously. If you have any concerns about Infisical or believe you have uncovered a vulnerability, please get in touch via the e-mail address security@infisical.com. In the message, try to provide a description of the issue and ideally a way of reproducing it. The security team will get back to you as soon as possible.

Note that this security address should be used only for undisclosed vulnerabilities. Please report any security problems to us before disclosing it publicly.
