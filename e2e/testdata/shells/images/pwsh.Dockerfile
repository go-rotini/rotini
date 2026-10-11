# Microsoft's PowerShell images are single-architecture, so this installs the release archive for
# the image's own architecture, checked against the release's hashes.
FROM debian:bookworm-20261005-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
ARG TARGETARCH
RUN set -eu; \
    case "$TARGETARCH" in \
        amd64) arch=x64; sum=ddbc4a2d113bbd46d283cfedcbcd117a70caefd7673f41f2b4e0000badf103bc ;; \
        arm64) arch=arm64; sum=924829e54c983648f6f1419a2dc7f9433c861b2fb5bd57736ff096c24f133729 ;; \
        *) echo "no PowerShell archive for $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl libicu72 libssl3 libgssapi-krb5-2 zlib1g; \
    curl -fsSL -o /tmp/pwsh.tar.gz "https://github.com/PowerShell/PowerShell/releases/download/v7.6.6/powershell-7.6.6-linux-$arch.tar.gz"; \
    echo "$sum  /tmp/pwsh.tar.gz" | sha256sum -c -; \
    mkdir -p /opt/pwsh; tar -xzf /tmp/pwsh.tar.gz -C /opt/pwsh; chmod +x /opt/pwsh/pwsh; \
    ln -s /opt/pwsh/pwsh /usr/local/bin/pwsh; \
    rm -rf /tmp/pwsh.tar.gz /var/lib/apt/lists/*; \
    groupadd rotinigroup; useradd -M -g rotinigroup rotiniuser
ENV POWERSHELL_TELEMETRY_OPTOUT=1 DOTNET_CLI_TELEMETRY_OPTOUT=1
