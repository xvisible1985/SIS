#!/bin/bash
set -e

cd /opt/sis

echo "=== [1/4] Pulling latest code ==="
git pull

echo "=== [2/4] Building frontend ==="
cd frontend
npm ci --silent
npm run build
cp -r dist/* /var/www/n2.novabot.ru/
cd ..
echo "Frontend deployed to /var/www/n2.novabot.ru/"

echo "=== [3/4] Building and restarting api-gateway ==="
docker compose build api-gateway
docker compose up -d api-gateway

echo "=== [4/4] Waiting for api-gateway to start ==="
sleep 3
docker logs sis-api-gateway-1 --tail 10

echo ""
echo "✅ Deploy complete! https://n2.novabot.ru"
