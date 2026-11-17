# Production deployment

One Ubuntu VPS with Docker runs three containers:

```
internet ──443──> caddy ──> api:8080 ──> postgres:5432
                    └─────> /srv/site (static website)
```

Caddy is the only container with published ports. It issues Let's Encrypt certificates for `API_DOMAIN` and `SITE_DOMAIN` on its own. The API and the database have no ports on the host, so the API can trust `X-Forwarded-For` (`TRUST_PROXY=true`) and the database cannot be reached from the internet.

## 1. DNS

At the domain registrar, point these records at the VPS IP:

| Type | Name | Value |
| --- | --- | --- |
| A | `api` | VPS IP |
| A | `@` | VPS IP |
| A | `www` | VPS IP |

Wait until `nslookup api.yolugra.tech` returns the VPS IP. Caddy cannot get a certificate before that.

## 2. Server preparation (once)

```bash
ssh root@<VPS-IP>

# Docker, if the OS template did not include it
curl -fsSL https://get.docker.com | sh

# Firewall: SSH and web only. Docker-published ports (80/443) bypass ufw, others stay closed.
ufw allow OpenSSH && ufw allow 80/tcp && ufw allow 443/tcp && ufw allow 443/udp && ufw --force enable

apt-get install -y git
git clone -b notification https://github.com/shalarasgar/carpool-backend.git /opt/yolugra
```

The repository is private if `git clone` asks for a password: use a GitHub personal access token with read access as the password, or copy the folder from Windows with `scp -r`.

## 3. Configuration

```bash
cd /opt/yolugra/deploy
cp .env.example .env
for v in POSTGRES_PASSWORD JWT_SECRET OTP_HMAC_SECRET ADMIN_TOKEN; do
  sed -i "s/^$v=.*/$v=$(openssl rand -hex 32)/" .env
done
chmod 600 .env
nano .env   # check the domains; leave OTP_TEST_PHONES empty
```

Keep a copy of `ADMIN_TOKEN` in a password manager. Losing `POSTGRES_PASSWORD` after the first start means resetting it inside the database, because Postgres stores the password it was initialised with.

Push notifications (optional):

```bash
mkdir -p /opt/yolugra/secrets
# from Windows: scp path\to\firebase-key.json root@<VPS-IP>:/opt/yolugra/secrets/fcm.json
chmod 644 /opt/yolugra/secrets/fcm.json   # the API runs as uid 65532 and must read it
# then in deploy/.env: FCM_CREDENTIALS_FILE=/secrets/fcm.json
```

Website: build it on Windows (`cd ../website && go run . -strict`) and copy `public/` to the server:

```bash
scp -r public/* root@<VPS-IP>:/opt/yolugra/deploy/site/
```

## 4. Start

```bash
cd /opt/yolugra/deploy
docker compose up -d --build
docker compose logs -f api caddy     # Ctrl+C to stop following
curl https://api.yolugra.tech/readyz
```

`/readyz` returns `"database":"up"` once the API is connected. Migrations run on start.

## 5. SMS gateway

Create the gateway key on the production server and enter it, with `https://api.yolugra.tech`, in the Android gateway app:

```bash
ADMIN_TOKEN=$(grep ^ADMIN_TOKEN= .env | cut -d= -f2)
curl -s -X POST https://api.yolugra.tech/admin/v1/gateways \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"main","operator_prefixes":["61","62","71"]}'
```

The key is shown once.

## 6. Flutter release build

```bash
flutter build appbundle --dart-define=API_BASE_URL=https://api.yolugra.tech
```

## Updates

```bash
cd /opt/yolugra && git pull && cd deploy && docker compose up -d --build
```

Database and photos live in the named volumes `yolugra_pgdata` and `yolugra_media` and survive rebuilds. `docker compose down -v` deletes them.

## Backups

```bash
chmod +x /opt/yolugra/deploy/backup.sh
(crontab -l 2>/dev/null; echo "30 3 * * * /opt/yolugra/deploy/backup.sh >>/var/log/yolugra-backup.log 2>&1") | crontab -
```

Dumps go to `/var/backups/yolugra` and are kept 14 days. They are on the same server, so download them now and then. Restore:

```bash
gunzip -c /var/backups/yolugra/db-<stamp>.sql.gz | docker compose exec -T postgres psql -U carpool -d carpool
```

Restore into an empty database (a fresh `pgdata` volume, API stopped), not over live data.
