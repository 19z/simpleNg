FROM debian:bookworm-slim

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends openssh-server \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /run/sshd /home/ssh-test/work \
    && useradd --create-home --shell /bin/bash ssh-test \
    && chown -R ssh-test:ssh-test /home/ssh-test \
    && printf 'ssh-test:changeme' | chpasswd \
    && printf '\nPasswordAuthentication yes\nPermitRootLogin no\n' >> /etc/ssh/sshd_config

EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
