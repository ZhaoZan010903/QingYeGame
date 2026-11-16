#!/bin/bash
# 仅维护已签发的清叶域名证书，不签发新证书，不接管旧站证书。
# 首次签发、配置启用及验收完成后，才允许启用对应 timer。
set -euo pipefail
umask 0077

CERTBOT=/opt/qingye-production/certbot/bin/certbot
NGINX=/opt/qingye-edge/current/sbin/nginx
SELF=/usr/local/lib/qingye-edge/renew-domain-certificates.sh
CONFIG=/etc/qingye-edge/letsencrypt
WORK=/var/lib/qingye-edge/letsencrypt
LOGS=/var/log/qingye-edge/letsencrypt
LINEAGE="$CONFIG/live/qingye-domains"
MARKER="$WORK/deployed-qingye-domains.sha256"

fail() { printf '%s\n' "$*" >&2; exit 1; }

deploy_domain() {
    local requested="${RENEWED_LINEAGE:-$LINEAGE}" cert key san name cert_public key_public digest previous
    # 未来旧站必须使用独立 qingye-legacy lineage 和专用发布步骤。
    # 它当前读取 /etc/qingye-edge/tls/legacy，不能仅 reload 就冒充已更新。
    [[ "$requested" == "$LINEAGE" ]] || fail '拒绝其他 lineage；旧站 qingye-legacy 需独立证书发布流程。'
    exec 8> "$WORK/.deploy.lock"
    flock -n 8 || fail '已有域名证书部署任务运行。'
    cert="$LINEAGE/fullchain.pem"
    key="$LINEAGE/privkey.pem"
    [[ -f "$cert" && -f "$key" ]] || fail '域名证书尚未完整签发，不启用域名续期任务。'
    timeout 20 openssl x509 -in "$cert" -checkend 86400 -noout
    san="$(timeout 20 openssl x509 -in "$cert" -ext subjectAltName -noout | tr ',' '\n' | sed -E 's/^[[:space:]]+//;s/[[:space:]]+$//')"
    for name in qingyegame.com www.qingyegame.com admin.qingyegame.com; do
        printf '%s\n' "$san" | grep -Fxq "DNS:$name" || fail "证书缺少明确 SAN：$name"
        timeout 20 openssl verify -purpose sslserver -verify_hostname "$name" -untrusted "$cert" "$cert"
    done
    cert_public="$(timeout 20 openssl x509 -in "$cert" -pubkey -noout | openssl pkey -pubin -outform DER | openssl dgst -sha256)"
    key_public="$(timeout 20 openssl pkey -in "$key" -passin pass: -pubout -outform DER | openssl dgst -sha256)"
    [[ "$cert_public" == "$key_public" ]] || fail '域名证书与私钥不匹配。'
    grep -Eq '^[[:space:]]*ssl_certificate[[:space:]]+/etc/qingye-edge/letsencrypt/live/qingye-domains/fullchain\.pem[[:space:]]*;' \
        /etc/qingye-edge/nginx.conf || fail '域名 TLS 配置尚未启用，拒绝标记为已加载；启用后重新运行部署钩子。'
    digest="$(sha256sum "$cert" | cut -d ' ' -f 1)"
    previous=''
    if [[ -f "$MARKER" && ! -L "$MARKER" ]]; then previous="$(cat "$MARKER")"; fi
    if [[ "$digest" == "$previous" ]]; then
        printf '%s\n' '域名证书未变化，无需重载入口。'
        return
    fi
    systemctl is-active --quiet qingye-edge.service
    "$NGINX" -t -p /var/lib/qingye-edge/ -c /etc/qingye-edge/nginx.conf
    systemctl reload qingye-edge.service
    systemctl is-active --quiet qingye-edge.service
    # 只有校验与加载全部成功才落标记，失败时下一轮继续部署已有证书。
    [[ ! -L "$MARKER" && ! -L "$MARKER.next" ]] || fail '部署标记不得是符号链接。'
    printf '%s\n' "$digest" > "$MARKER.next"
    chmod 0600 "$MARKER.next"
    mv -f -- "$MARKER.next" "$MARKER"
    printf '%s\n' '清叶域名证书已验证并加载；IP WebTransport 服务未重启。'
}

[[ "${EUID}" -eq 0 ]] || fail '需要 root 权限。'
[[ -d "$CONFIG" && -d "$WORK" && -d "$LOGS" ]] || fail '域名证书专用目录尚未初始化。'
case "${1:-renew}" in
    --deploy-hook)
        [[ "$#" -eq 1 ]] || fail '无效参数。'
        deploy_domain
        ;;
    renew)
        [[ "$#" -le 1 ]] || fail '无效参数。'
        [[ -f "$CONFIG/renewal/qingye-domains.conf" && -f "$LINEAGE/fullchain.pem" ]] || fail '域名证书尚未签发；请保持 timer 未启用。'
        exec 9> "$WORK/.renew.lock"
        flock -n 9 || fail '已有域名证书维护任务运行。'
        "$CERTBOT" renew --config-dir "$CONFIG" --work-dir "$WORK" --logs-dir "$LOGS" \
            --cert-name qingye-domains --quiet --no-random-sleep-on-renew \
            --deploy-hook "/bin/bash $SELF --deploy-hook"
        # 签发成功但前次加载失败时，本轮即使不续签也重试加载。
        unset RENEWED_LINEAGE
        deploy_domain
        ;;
    *) fail '用法：renew-domain-certificates.sh [renew|--deploy-hook]' ;;
esac
