#!/bin/sh
set -eu

: "${DOMAIN:?Set DOMAIN to the public hostname assigned to this shell}"
: "${ROOTPWD:?Set ROOTPWD to a non-empty root password}"

if [ ! -f /etc/simpleng-password-initialized ]; then
    printf 'root:%s\n' "$ROOTPWD" | chpasswd
    touch /etc/simpleng-password-initialized
fi
unset ROOTPWD
mkdir -p /run/sshd /etc/ssh/sshd_config.d
printf 'PermitRootLogin yes\nPasswordAuthentication yes\n' > /etc/ssh/sshd_config.d/00-simpleng.conf
ssh-keygen -A
/usr/sbin/sshd -t
/usr/sbin/sshd
simpleNg --domain "$DOMAIN" --local ssh:// &
client_pid=$!

shutdown() {
    kill "$client_pid" 2>/dev/null || true
    wait "$client_pid" 2>/dev/null || true
}
trap shutdown EXIT
trap 'exit 0' INT TERM

echo "Online shell is connecting for $DOMAIN"
wait "$client_pid"
