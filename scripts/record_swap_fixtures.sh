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
L=https://li.quest/v1
K=(-H "x-lifi-api-key: $(cat "${LIFI_API_KEY_FILE:?}")")
C=1201081091099710
XLM=CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA
USDC_C=CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75
EURC_C=CDTKPWPLOURQA2SGTKTUQOWRCBZEORB4BWBOMJ3D3ZTQQSGE5F6JBQLV
SMALL=GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN7   # ~99 XLM, no USDC trustline
BIG=GDHF3HIKWM5KJAVLZBSZWUFDOEOT7IBMY22UXG4QBE326O354INLPAND     # large XLM balance
q() { curl -s "${K[@]}" "$L/quote?fromChain=$C&toChain=$C&fromToken=$1&toToken=$2&fromAmount=$3&fromAddress=$4&slippage=0.005"; }
q $XLM $USDC_C 100000000     $SMALL > "$out/lifi_quote_xlm_usdc_trust.json"   # 10 XLM, trust() in auth tree
q $XLM $EURC_C 1000000000000 $BIG   > "$out/lifi_quote_xlm_eurc_split.json"   # Soroswap + Aqua multi-hop
q $XLM $USDC_C 1000000000000 $BIG   > "$out/lifi_quote_xlm_usdc_100k.json"    # Aqua + Sushi split
q $XLM $USDC_C 100000000000  $SMALL > "$out/lifi_quote_shrink.json"           # 10,000 XLM from ~99 XLM account
q $XLM CB23WRDQWGSP6YPMY4UV5C4OW5CBTXKYN3XEATG7KJEZCXMJBYEHOUOV 10000000 $BIG > "$out/lifi_err_1003.json"
curl -s "${K[@]}" "$L/token?chain=$C&token=CBI7UCH5KGSVQRO5H4SUCZUTZABCITZLRHQQZTWL2TK4RZ72TAR6IHRV" > "$out/lifi_token_dejtrsy.json"
