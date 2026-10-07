# hive — launcher dos produtos HIVE

Release preparada: **v1.2.0**, branch `release/v1.2.0`.

`hive` instala, atualiza, reverte e executa os produtos HIVE: Hive Mind e Hive Atlas. Ele baixa cada um da release assinada do repositório do produto, verifica e só então ativa.

## Instalar

```bash
curl -fsSL https://github.com/H-I-V-E-Tec/hive_cli/releases/latest/download/install.sh | sh
hive install mind
hive install atlas  # cliente Go; releases Python anteriores continuam suportadas
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
| `hive atlas` | Inicia a ponte stdio para o MCP remoto do Atlas; usa a sessão única. |
| `hive atlas version --json` | Identidade do cliente Atlas instalado (tag e SHA do commit). |

Comandos que o launcher não conhece (`setup`, `doctor`, `search`…) são repassados ao Hive Mind. Ao repassar, o launcher define `HIVE_LAUNCHER`, e o `hive setup` registra no agente o caminho estável do launcher (`hive mind`), que continua válido depois de cada update.

O Atlas usa o comando estável `hive atlas`; registre no agente o caminho absoluto
do launcher com argumentos `["atlas"]`. `hive setup` continua registrando o Mind.
As releases atuais do Atlas usam binários Go. Para releases antigas `.pyz`, o launcher usa `python3` ou `python` do PATH (no Windows, tenta primeiro `py -3`),
em modo isolado, e exige Python 3.10 ou superior. Não precisa de dependências
Python. No cliente Go, `hive login` fornece a sessão compartilhada,
e `hive atlas` encaminha todas as ferramentas para `/atlas/mcp`. Evidência
sanitizada é avaliada no servidor durante a chamada; feedback fica no serviço,
separado por membro. A biblioteca embutida só é usada com `--offline` explícito.
Veja o [guia do Atlas](../hive_atlas/docs/deploy.md) para o registro no agente.

### Releases privadas

Para instalar produtos de um repositório privado, defina `GH_TOKEN` (ou
`GITHUB_TOKEN`) no ambiente do launcher, com acesso ao repositório e permissão
**Contents: read**. `GH_TOKEN` tem prioridade. Com credencial, o launcher consulta
a release pela API e baixa o asset pelo ID, com `Accept: application/octet-stream`;
o token não é encaminhado ao CDN nos redirects. A verificação Sigstore e o
checksum continuam obrigatórios. Sem credencial, mantém o download público.

No Bash, informe o token sem gravá-lo no histórico:

```bash
read -r -s -p 'GitHub token (Contents: read): ' GH_TOKEN
printf '\n'
export GH_TOKEN
hive install atlas
unset GH_TOKEN
```

O token é necessário para baixar/atualizar releases privadas, não para executar
o MCP já instalado. No servidor, o bootstrap usa o arquivo privado
`/srv/hive-private/github-release.token`; esse arquivo não é enviado ao cliente.

## Segurança

- **Origem fixa no código.** `internal/registry` define, para cada produto, o repositório, o nome dos pacotes e a identidade de assinatura aceita: `https://github.com/<repo>/.github/workflows/release.yml@refs/tags/<versão>`, emitida por `https://token.actions.githubusercontent.com`. Nada fora do binário muda isso.
- **Assinatura sempre verificada** com `sigstore-go`: certificado Fulcio com SCT, entrada no log de transparência Rekor e carimbo de tempo observado. A raiz de confiança do Sigstore é obtida por TUF e guardada em `~/.hive/sigstore`.
- **Checksum** do pacote conferido contra o `SHA256SUMS` assinado.
- **Sem downgrade** silencioso: uma versão mais antiga exige `--allow-downgrade`.
- **Ativação atômica:** o executável ou zipapp é testado (`<produto> version`) antes de virar a versão ativa; a anterior fica em disco para `hive rollback`.
- **Extração defensiva:** só o executável esperado é lido; caminhos com `..`, absolutos, symlinks e entradas duplicadas abortam a instalação.
- **Transporte:** somente HTTPS, redirect para HTTP recusado, limites de tamanho e tempo.
- `~/.hive` é privado (0700) e é recusado se outros usuários puderem escrever nele. Não há telemetria nem atualização automática.

## Contrato de release de um produto

Para entrar no registro, a release `vX.Y.Z` de um produto precisa publicar:

1. `<prefixo>-vX.Y.Z-<os>-<arch>.tar.gz` (`.zip` no Windows), com o executável na raiz do pacote. As plataformas são `linux`, `darwin` e `windows`, com `amd64` e `arm64`.
2. `SHA256SUMS` no formato do `sha256sum`, com o nome do arquivo sem `./`.
3. `SHA256SUMS.sigstore-bundle.json`: bundle Sigstore **padrão** (`cosign sign-blob --new-bundle-format`) sobre o `SHA256SUMS`, assinado pelo workflow `.github/workflows/release.yml` do próprio repositório, rodando na tag.
4. O executável deve responder a `version` (ou aos `SmokeArgs` do registro) com JSON contendo `{"version": "vX.Y.Z"}`.

Produtos Python usam `Runtime: PythonZipapp` no registro e publicam um único
`<prefixo>-vX.Y.Z.pyz` para todas as plataformas (Atlas: `atlas-vX.Y.Z.pyz`).
O zipapp contém código, biblioteca e identidade de release. Ele usa os mesmos
`SHA256SUMS`, bundle Sigstore padrão, verificação de origem e teste de versão
dos produtos nativos. O pacote completo é mantido sem extração e executado com
Python 3.10+, inclusive durante instalação e rollback. A ausência do runtime ou
um teste de versão falho impede a ativação.

Releases que publicam só o bundle antigo do cosign (`SHA256SUMS.sigstore.json`) são recusadas pelo launcher.

## Desenvolver

```bash
go test -race ./...
CGO_ENABLED=0 go build -o hive .
HIVE_HOME=$(mktemp -d) ./hive version
```

`HIVE_HOME` troca o diretório `~/.hive`, para testar sem tocar na instalação real.

### Atlas Go e Center

O launcher prefere o cliente Go listado no manifesto de checksums assinado.
Releases anteriores contendo somente `.pyz` continuam instaláveis, e rollback
resolve o formato de cada versão instalada. Falha de download/checksum de um
asset Go listado nunca provoca fallback para Python.

```bash
hive update hive
hive install atlas
hive login
hive setup atlas --client codex
hive doctor atlas
hive atlas
```

`hive login` e `hive logout` são comandos do launcher, sem dependência de instalar
produtos. O login solicita JWT RS256 `aud=hive` com as permissões efetivas do
membro e verifica sua assinatura/JWKS antes de guardar `$HIVE_HOME/token`.
Mind e Atlas usam o mesmo arquivo; `HIVE_TOKEN` e `HIVE_TOKEN_FILE` permitem
injeção da sessão. `hive login --check` valida o token atual.

O serviço Mind exige `product.mind`; o Atlas exige `product.atlas`. Um membro
com acesso somente ao Atlas consegue autenticar sem instalar o Mind. Os atalhos
setup/doctor despacham ao produto. `hive atlas` é uma ponte para o MCP remoto
Streamable HTTP `<center>/atlas/mcp`; observe, busca e feedback rodam no servidor.

As releases do Atlas continuam públicas e não exigem token GitHub.
