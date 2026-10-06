# hive — launcher dos produtos HIVE

`hive` instala, atualiza, reverte e executa os produtos HIVE (hoje: Hive Mind). Ele não contém nenhum produto: baixa cada um da release assinada do repositório do produto, verifica e só então ativa.

## Instalar

```bash
curl -fsSL https://github.com/H-I-V-E-Tec/hive_cli/releases/latest/download/install.sh | sh
hive install mind
hive login     # usuário e senha do HIVE Center
hive setup     # registra o Mind nos agentes encontrados (Claude Code, Claude Desktop, Codex)
hive doctor
```

Nenhuma URL precisa ser informada: o Mind já vem com o HIVE Center padrão.

O bootstrap instala só o launcher em `~/.hive/bin` (sem `sudo`) e põe essa pasta no `PATH` dos terminais novos (`.zshrc`, `.bashrc`, `.profile`; o arquivo do shell atual é criado se não existir). Quando `~/.local/bin` já está no `PATH`, como na maioria das distribuições Linux, cria também o atalho `~/.local/bin/hive`, e o comando funciona no mesmo terminal. Com `cosign` instalado, ele também verifica a assinatura do próprio launcher; sem ele, verifica o checksum.

## Comandos

| Comando | O que faz |
|---|---|
| `hive install <produto> [--version vX.Y.Z] [--allow-downgrade]` | Baixa, verifica e ativa. |
| `hive update [<produto>...] [--check]` | Sem argumentos, atualiza todos os produtos instalados e o próprio `hive`. |
| `hive rollback <produto>` | Reativa a versão anterior mantida em disco; funciona sem rede. |
| `hive uninstall <produto>` | Remove o produto. Login e configurações em `~/.hive` ficam. |
| `hive version [--json]` | Versões do launcher e dos produtos; mostra atualização disponível conhecida há menos de 24 h. |
| `hive list` | Produtos disponíveis. |
| `hive <produto> [args]` | Executa o produto. `hive mind` sem argumentos inicia o MCP. |

Comandos que o launcher não conhece (`login`, `setup`, `doctor`, `search`…) são repassados ao Hive Mind. Ao repassar, o launcher define `HIVE_LAUNCHER`, e o `hive setup` registra no agente o caminho estável do launcher (`hive mind`), que continua válido depois de cada update.

## Segurança

- **Origem fixa no código.** `internal/registry` define, para cada produto, o repositório, o nome dos pacotes e a identidade de assinatura aceita: `https://github.com/<repo>/.github/workflows/release.yml@refs/tags/<versão>`, emitida por `https://token.actions.githubusercontent.com`. Nada fora do binário muda isso.
- **Assinatura sempre verificada** com `sigstore-go`: certificado Fulcio com SCT, entrada no log de transparência Rekor e carimbo de tempo observado. A raiz de confiança do Sigstore é obtida por TUF e guardada em `~/.hive/sigstore`.
- **Checksum** do pacote conferido contra o `SHA256SUMS` assinado.
- **Sem downgrade** silencioso: uma versão mais antiga exige `--allow-downgrade`.
- **Ativação atômica:** o binário é testado (`<produto> version`) antes de virar a versão ativa; a anterior fica em disco para `hive rollback`.
- **Extração defensiva:** só o executável esperado é lido; caminhos com `..`, absolutos, symlinks e entradas duplicadas abortam a instalação.
- **Transporte:** somente HTTPS, redirect para HTTP recusado, limites de tamanho e tempo.
- `~/.hive` é privado (0700) e é recusado se outros usuários puderem escrever nele. Não há telemetria nem atualização automática.

## Contrato de release de um produto

Para entrar no registro, a release `vX.Y.Z` de um produto precisa publicar:

1. `<prefixo>-vX.Y.Z-<os>-<arch>.tar.gz` (`.zip` no Windows), com o executável na raiz do pacote. As plataformas são `linux`, `darwin` e `windows`, com `amd64` e `arm64`.
2. `SHA256SUMS` no formato do `sha256sum`, com o nome do arquivo sem `./`.
3. `SHA256SUMS.sigstore-bundle.json`: bundle Sigstore **padrão** (`cosign sign-blob --new-bundle-format`) sobre o `SHA256SUMS`, assinado pelo workflow `.github/workflows/release.yml` do próprio repositório, rodando na tag.
4. O executável deve responder a `version` (ou aos `SmokeArgs` do registro) com JSON contendo `{"version": "vX.Y.Z"}`.

Releases que publicam só o bundle antigo do cosign (`SHA256SUMS.sigstore.json`) são recusadas pelo launcher.

## Desenvolver

```bash
go test -race ./...
CGO_ENABLED=0 go build -o hive .
HIVE_HOME=$(mktemp -d) ./hive version
```

`HIVE_HOME` troca o diretório `~/.hive`, para testar sem tocar na instalação real.
