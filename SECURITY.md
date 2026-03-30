# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.1.x   | :white_check_mark: |

## Reporting a Vulnerability

If you discover a security vulnerability in OnScreen, **please do not open a public issue**.

Instead, report it privately:

1. **Email**: Send details to **security@onscreen.dev**
2. **GitHub**: Use [GitHub's private vulnerability reporting](https://github.com/onscreen/onscreen/security/advisories/new)

Please include:

- A description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

## Response Timeline

- **Acknowledgement**: Within 48 hours
- **Initial assessment**: Within 1 week
- **Fix timeline**: Depends on severity
  - **Critical** (RCE, auth bypass, data leak): Patch within 72 hours
  - **High** (privilege escalation, XSS): Patch within 2 weeks
  - **Medium/Low**: Next scheduled release

## Disclosure Policy

- We follow [coordinated disclosure](https://en.wikipedia.org/wiki/Coordinated_vulnerability_disclosure).
- We will credit reporters in the release notes (unless you prefer anonymity).
- We ask that you give us reasonable time to fix the issue before public disclosure.

## Security Best Practices for Operators

- Always run OnScreen behind a reverse proxy with TLS (nginx, Caddy, Traefik).
- Use a strong `SECRET_KEY` (at least 32 random bytes). Generate one with: `openssl rand -hex 32`
- Keep PostgreSQL and Valkey on a private network, not exposed to the internet.
- Use unique, strong passwords for database and SMTP credentials.
- Restrict OAuth redirect URIs to your exact `BASE_URL`.
- Keep OnScreen updated to the latest release.
