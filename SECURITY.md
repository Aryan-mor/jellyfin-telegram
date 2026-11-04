# Security Policy

## Supported versions

Only the latest commit on `main` receives security fixes.

## Reporting a vulnerability

Please **do not open a public issue** for security problems.

Report privately through
[GitHub private vulnerability reporting](https://github.com/Aryan-mor/jellyfin-telegram/security/advisories/new).
Include a description, steps to reproduce and the impact. Do not include real tokens or
credentials.

You can expect an acknowledgement within a few days. Once a fix is ready it will be released and
the advisory published, with credit if you want it.

## Things to know when running jellyfin-telegram

- `.env` holds your bot tokens and `api_hash`; `DATA_DIR` holds Telegram sessions. Keep both
  private and never commit them.
- Signed-in user-account session files are not encrypted and grant full account access. Use a
  separate Telegram account for the Downloads and Channels features.
- The stream token is visible to Jellyfin users and in Jellyfin logs. Rotate it by deleting
  `DATA_DIR/gateway.token` and restarting.
- The service is plain HTTP. Do not expose port 8097 to the public internet; use your LAN or a
  VPN such as Tailscale.