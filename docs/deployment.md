# BotSurv Paper Deployment

This deployment path is intentionally paper-only.

## VPS Bootstrap

Run once on the VPS as root:

```bash
bash deploy/scripts/bootstrap-vps.sh
```

The bootstrap script installs PostgreSQL, creates:

- system user: `botsurv`
- app directory: `/opt/botsurv`
- env file: `/etc/botsurv/paper.env`
- database/user: `botsurv`

Edit `/etc/botsurv/paper.env` if you want to change credentials or optional integrations.

## GitHub Secrets

Set these repository secrets:

```text
VPS_HOST=178.105.27.231
VPS_USER=root
VPS_SSH_PRIVATE_KEY=<private key allowed to SSH into VPS>
```

Use a deploy key dedicated to this repo if practical.

## CI

CI runs on every push and pull request:

```bash
go test -count=1 ./...
go vet ./...
go test -race -count=1 ./internal/broker ./internal/risk ./internal/cli ./internal/scheduler ./internal/marketdata
go build ./cmd/bot
```

## Manual CD

Deployment is manual through GitHub Actions:

```text
Actions -> Deploy Paper -> Run workflow
```

The workflow:

1. Runs tests and vet.
2. Builds a Linux binary.
3. Uploads a release bundle to the VPS.
4. Installs/updates the systemd service.
5. Runs config validation.
6. Runs migrations.
7. Restarts `botsurv-paper.service`.

The VPS service uses:

```text
/opt/botsurv/app/configs/paper.vps.yaml
/etc/botsurv/paper.env
```

## Operations

Check service status:

```bash
systemctl status botsurv-paper.service --no-pager
```

Tail logs:

```bash
journalctl -u botsurv-paper.service -f
```

Restart:

```bash
systemctl restart botsurv-paper.service
```

Stop:

```bash
systemctl stop botsurv-paper.service
```

## Safety Notes

- Do not deploy live mode through this workflow.
- Keep `ENABLE_LIVE_TRADING=false`.
- Do not put real secrets in committed config files.
- Paper mode must fail closed if market data bootstrap fails.

