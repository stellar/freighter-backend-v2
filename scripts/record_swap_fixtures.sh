#!/usr/bin/env bash
# scripts/record_swap_fixtures.sh — re-record swap fixtures from mainnet.
# Usage: LIFI_API_KEY_FILE=~/.lifi_api_key scripts/record_swap_fixtures.sh
set -euo pipefail
out=internal/services/swap/testdata
mkdir -p "$out"
H=https://horizon.stellar.org
USDC=USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN
curl -sf "$H/paths/strict-send?source_asset_type=native&source_amount=100&destination_assets=$USDC" > "$out/horizon_strict_send_xlm_usdc.json"
curl -sf "$H/paths/strict-receive?source_assets=$USDC&destination_asset_type=native&destination_amount=0.5" > "$out/horizon_strict_receive_usdc_xlm.json"
# LI.FI fixtures are added in Task 10.
