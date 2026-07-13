# Security Policy

## Reporting a Vulnerability

The Loopback Gateway maintainers take security issues seriously. We appreciate your efforts to responsibly disclose any vulnerabilities you find.

**Please do NOT report security vulnerabilities through public GitHub issues.**

Instead, please report them privately via **GitHub Security Advisories**: use [GitHub's private vulnerability reporting](https://github.com/haj/loopback-llm-gateway/security/advisories/new) to submit a report directly through this repository.

### What to include

To help us triage and respond quickly, please include:

- A description of the vulnerability and its potential impact
- Step-by-step instructions to reproduce the issue
- Affected version(s) and component(s) (e.g., `core`, `transports`, `plugins/*`)
- Any relevant configuration or environment details
- Proof-of-concept code, if available

### What to expect

- **Acknowledgment**: We will acknowledge receipt of your report within **72 hours**.
- **Updates**: We will provide status updates as we investigate.
- **Resolution**: Once a fix is available, we will coordinate with you on disclosure timing.
- **Credit**: We are happy to credit reporters in our release notes and security advisories (unless you prefer to remain anonymous).

## Supported Versions

Loopback Gateway is a young project; security patches land on the latest release only. We recommend always running the latest version.

Note that Loopback Gateway is a fork of [Bifrost](https://github.com/maximhq/bifrost). Vulnerabilities in inherited upstream code are also worth reporting upstream; vulnerabilities in fork-specific subsystems (guardrails, RBAC, audit logging, SSO/SCIM, circuit breaker, data connectors) belong here.

## Security Considerations

Loopback Gateway is an AI gateway that routes requests to multiple LLM providers. When deploying it, keep the following in mind:

- **API Key Management**: The gateway handles provider API keys. Ensure keys are stored securely and never committed to version control. Use environment variables or a secrets manager.
- **Network Exposure**: Restrict access to the admin interface and API endpoints using firewalls, VPNs, or authentication layers appropriate for your environment.
- **TLS**: Always use TLS when exposing the gateway to external networks.
- **Access Control**: Enable RBAC and use virtual keys and access profiles to enforce least-privilege access to upstream providers. The built-in secure-setup flow detects insecure defaults and can enforce RBAC in one step.
- **Plugin Security**: Only use plugins from trusted sources. Plugins execute within the request pipeline and have access to request/response data.

## Disclosure Policy

We follow a coordinated disclosure process:

1. The reporter submits the vulnerability privately.
2. We confirm the issue and develop a fix.
3. We release the fix and publish a security advisory.
4. The vulnerability details are made public after users have had reasonable time to update (typically 30 days after the fix is released).

We kindly ask that you do not publicly disclose the vulnerability until we have had a chance to address it.

## Scope

The following are **in scope** for security reports:

- The gateway itself (core, transports, framework, CLI)
- Plugins in this repository (`plugins/` directory)
- The web UI (`ui/`)

The following are **out of scope**:

- Social engineering attacks
- Denial of service attacks that rely purely on volumetric traffic
