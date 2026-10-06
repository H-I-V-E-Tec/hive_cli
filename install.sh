#!/bin/sh
# Bootstrap of the HIVE launcher: installs only `hive` into ~/.hive/bin.
# Products are installed afterwards with `hive install <produto>`, which
# verifies their Sigstore signatures itself.
set -eu

REPO="${HIVE_CLI_REPOSITORY:-H-I-V-E-Tec/hive_cli}"
HIVE_HOME="${HIVE_HOME:-$HOME/.hive}"
BIN_DIR="$HIVE_HOME/bin"
VERSION_RE='^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$'

info() { printf 'info: %s\n' "$1"; }
warn() { printf 'aviso: %s\n' "$1" >&2; }
die() { printf 'erro: %s\n' "$1" >&2; exit 1; }

case "$(uname -s)" in
    Darwin) OS=darwin ;;
    Linux) OS=linux ;;
    *) die "sistema não suportado por este instalador: $(uname -s) (no Windows, baixe o .zip da release)" ;;
esac
case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    arm64|aarch64) ARCH=arm64 ;;
    *) die "arquitetura não suportada: $(uname -m)" ;;
esac
for cmd in curl tar; do
    command -v "$cmd" >/dev/null 2>&1 || die "'$cmd' é necessário"
done
if command -v sha256sum >/dev/null 2>&1; then
    sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
    sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
    die "'sha256sum' ou 'shasum' é necessário para verificar o download"
fi

VERSION="${HIVE_CLI_VERSION:-}"
if [ -z "$VERSION" ]; then
    LATEST_URL=$(curl -fsSLI --proto '=https' --tlsv1.2 -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") \
        || die "não foi possível consultar a última release de $REPO"
    VERSION="${LATEST_URL##*/}"
fi
printf '%s' "$VERSION" | grep -Eq "$VERSION_RE" || die "versão inválida: '$VERSION'"

ASSET="hive-cli-$VERSION-$OS-$ARCH.tar.gz"
BASE="https://github.com/$REPO/releases/download/$VERSION"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

info "baixando hive $VERSION ($OS/$ARCH)"
for file in "$ASSET" SHA256SUMS; do
    curl -fsSL --proto '=https' --tlsv1.2 -o "$TMP/$file" "$BASE/$file" || die "falha ao baixar $file"
done

EXPECTED=$(awk -v asset="$ASSET" '$2 == asset || $2 == "*" asset {print $1}' "$TMP/SHA256SUMS")
[ -n "$EXPECTED" ] || die "$ASSET não está listado em SHA256SUMS"
[ "$(sha256 "$TMP/$ASSET")" = "$EXPECTED" ] || die "checksum de $ASSET não confere; download recusado"
info "checksum verificado"

if command -v cosign >/dev/null 2>&1; then
    curl -fsSL --proto '=https' --tlsv1.2 -o "$TMP/SHA256SUMS.sigstore.json" "$BASE/SHA256SUMS.sigstore.json" \
        || die "falha ao baixar a assinatura"
    cosign verify-blob \
        --bundle "$TMP/SHA256SUMS.sigstore.json" \
        --certificate-identity "https://github.com/$REPO/.github/workflows/release.yml@refs/tags/$VERSION" \
        --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
        "$TMP/SHA256SUMS" >/dev/null 2>&1 || die "assinatura Sigstore inválida; download recusado"
    info "assinatura Sigstore verificada (release.yml@$VERSION)"
else
    warn "cosign não encontrado: verificado apenas o checksum. Os produtos instalados depois pelo 'hive' têm a assinatura sempre verificada."
fi

mkdir -p "$TMP/x"
tar -xzf "$TMP/$ASSET" -C "$TMP/x" hive || die "pacote não contém o binário 'hive'"
if [ ! -f "$TMP/x/hive" ] || [ -L "$TMP/x/hive" ]; then
    die "binário inválido no pacote"
fi
"$TMP/x/hive" version --json >/dev/null 2>&1 || die "o binário baixado não executa nesta máquina"

umask 077
mkdir -p "$BIN_DIR"
chmod 700 "$HIVE_HOME"
mv -f "$TMP/x/hive" "$BIN_DIR/hive"
chmod 755 "$BIN_DIR/hive"
info "instalado em $BIN_DIR/hive"

# A `curl | sh` cannot change the PATH of the shell that ran it. When
# ~/.local/bin is already on PATH (default on most Linux distributions), a
# symlink there makes `hive` usable right away.
LOCAL_BIN="$HOME/.local/bin"
LINKED=""
case ":$PATH:" in
    *":$LOCAL_BIN:"*)
        if [ -L "$LOCAL_BIN/hive" ] && [ "$(readlink "$LOCAL_BIN/hive")" = "$BIN_DIR/hive" ]; then
            LINKED=1
        elif [ -e "$LOCAL_BIN/hive" ] || [ -L "$LOCAL_BIN/hive" ]; then
            warn "$LOCAL_BIN/hive já existe e não aponta para o launcher; não foi alterado"
        else
            mkdir -p "$LOCAL_BIN"
            ln -s "$BIN_DIR/hive" "$LOCAL_BIN/hive"
            LINKED=1
            info "atalho criado em $LOCAL_BIN/hive"
        fi
        ;;
esac

LINE="export PATH=\"$BIN_DIR:\$PATH\""
case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *)
        # Create the rc file of the current shell when it does not exist yet,
        # so new terminals find ~/.hive/bin.
        case "${SHELL:-}" in
            */zsh) SHELL_RC="$HOME/.zshrc" ;;
            */bash) SHELL_RC="$HOME/.bashrc" ;;
            *) SHELL_RC="" ;;
        esac
        if [ -n "$SHELL_RC" ] && [ ! -e "$SHELL_RC" ]; then
            : > "$SHELL_RC"
        fi
        for rc in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.profile"; do
            if [ -f "$rc" ] && ! grep -Fqx "$LINE" "$rc"; then
                printf '\n# HIVE launcher\n%s\n' "$LINE" >> "$rc"
                info "PATH atualizado em $rc"
            fi
        done
        if [ -z "$LINKED" ]; then
            warn "abra um terminal novo (ou rode: $LINE) para usar o comando hive"
        fi
        ;;
esac

OTHER=$(command -v hive 2>/dev/null || true)
if [ -n "$OTHER" ] && [ "$OTHER" != "$BIN_DIR/hive" ] && { [ -z "$LINKED" ] || [ "$OTHER" != "$LOCAL_BIN/hive" ]; }; then
    warn "outro 'hive' foi encontrado em $OTHER (provavelmente o Hive Mind antigo). Remova-o para usar o launcher: rm $OTHER"
fi

cat <<EOF

hive $VERSION instalado. Próximos passos:
  hive install mind
  hive login
  hive setup
  hive doctor
EOF
