# Kindle Manager: pesquisa técnica (KOReader + SSH/SFTP no Kindle)

Data: 2026-10-05. Fontes primárias lidas direto do código (clones shallow):

- `koreader/koreader` @ `2ce49e1` (master, 2026-10-05). Abaixo aparece como **KO**.
- `koreader/koreader-base` @ `ffac076`. Abaixo aparece como **KOB**.
- `koreader/koreader.wiki` @ `00f14aa`.
- `notmarek/kindle-usbnetlite` @ `62532ab`.
- Release `koreader-kindlehf-v2026.07.1.zip`: baixei o zip e listei o conteúdo.
- kindlemodding.org e MobileRead (wiki e fórum).

Legenda: **[código]** = confirmado no código-fonte; **[doc]** = confirmado em documentação primária; **[inferido]** = deduzido do código, sem teste em aparelho; **[NÃO CONFIRMADO]** = não achei fonte primária.

Links base:
- KO: https://github.com/koreader/koreader/blob/2ce49e115fc1bcc47b473078c549e59b3922105b/
- KOB: https://github.com/koreader/koreader-base/blob/ffac0764ee70a4226f9cb8884438fdde0fd95a74/

---

## 1. Plugin SSH do KOReader

| Item | Fato | Fonte |
|---|---|---|
| Servidor | **dropbear** com patches do KOReader (versão no master: `DROPBEAR_2026.94`). O banner de identificação é `dropbear_<ver> (KOReader)` | [código] KOB `thirdparty/dropbear/CMakeLists.txt`, `localoptions.h.cmake` |
| Menu | Ícone da engrenagem (Settings) → **Network** → **SSH server**. Tem checkbox para ligar, porta, "SSH public key", "Login with key only", "Login without password (DANGEROUS)", "Start SSH server with KOReader" e "Force close all connections on stop" | [código] KO `plugins/SSH.koplugin/main.lua` e `frontend/ui/elements/filemanager_menu_order.lua` (setting → network → `ssh`); [doc] wiki [SSH](https://github.com/koreader/koreader/wiki/SSH) |
| Porta padrão | **2222** (`G_reader_settings` chave `SSH_port`, que dá para mudar no menu) | [código] `SSH.koplugin/main.lua` (`or "2222"`), KOB `DROPBEAR_DEFPORT "2222"` |
| Linha de comando | `./dropbear -E -R -p <porta> -P /tmp/dropbear_koreader.pid`, mais `-s` (só chave) **ou** `-n` (sem senha). `-R` gera as host keys sob demanda e as grava em `settings/SSH/dropbear_{rsa,ecdsa,ed25519}_host_key`, então a host key persiste entre reinícios e o fingerprint fica estável | [código] `main.lua`; KOB `CMakeLists.txt` (`*_PRIV_FILENAME`) |
| Firewall | No Kindle o plugin abre a porta com `iptables -A INPUT -p tcp --dport <porta> ... ACCEPT` e fecha com `-D` no stop | [código] `main.lua` |
| Usuário | Na prática é **root**: o KOReader roda como root no Kindle e a wiki manda usar `root`. O patch `-n` aceita **qualquer senha** | [doc] wiki SSH ("Use `root` as both username and password"); [código] KOB `nopasswd-hack.patch` |
| Senha sem `-n` | O dropbear valida contra o `/etc/shadow` do Kindle, e a senha de root do firmware é desconhecida. **Não dá para depender disso.** Use chave ou `-n` | [inferido]; valor da senha [NÃO CONFIRMADO] |
| authorized_keys | Caminho relativo **`settings/SSH/authorized_keys`**. No Kindle fica em **`/mnt/us/koreader/settings/SSH/authorized_keys`**. O patch desliga a checagem de permissão de dono/grupo (o vfat não suporta chmod) | [código] KOB `pubkey-hack.patch` (`authorized_keys_dir = "settings/SSH"`), `nochdir-hack.patch` (`daemon(1, …)` mantém o cwd em `/mnt/us/koreader`); KOB `CMakeLists.txt` `PLATFORM_KOREADER_DIR=/mnt/us/koreader` |
| Tipo de chave | A wiki recomenda `ecdsa` por causa da issue antiga [#8370](https://github.com/koreader/koreader/issues/8370). O dropbear atual negocia `ssh-ed25519` como host key, como mostra o log em [#13681](https://github.com/koreader/koreader/issues/13681). Chave de cliente ed25519 provavelmente funciona | [doc] wiki; [NÃO CONFIRMADO] para ed25519 de cliente no Kindle |
| Autostart | Existe: "Start SSH server with KOReader" (chave `SSH_autostart`) chama `start()` no `init` do plugin, ou seja, **quando o KOReader abre**. Não sobe no boot do Kindle | [código] `main.lua` |
| Ativo só com o KOReader aberto? | O dropbear **daemoniza** e é um processo separado. O plugin **não tem** handler `onExit`/`onSuspend` e o `koreader.sh` não mata o dropbear. Conclusão provável: **o servidor continua rodando depois de sair do KOReader**, até o reboot ou um stop explícito. Além disso, a extensão KUAL do KOReader tem "Start/Stop SSH server", que roda o mesmo dropbear na porta 2222 **sem o KOReader aberto** | [inferido] `main.lua` + `platform/kindle/koreader.sh`; [código] `platform/kindle/extensions/koreader/{menu.json,bin/koreader-ext.sh}` (`start_ssh`/`stop_ssh`) |
| **SFTP** | **Funciona.** O KOReader empacota o `sftp-server` do OpenSSH (10.4p1) e o dropbear foi compilado com `SFTPSERVER_PATH=/mnt/us/koreader/sftp-server`. No zip kindlehf v2026.07.1 estão `koreader/dropbear`, `koreader/sftp-server`, `koreader/scp` e `koreader/dbclient` | [código] KOB `thirdparty/openssh/CMakeLists.txt`, `thirdparty/dropbear/CMakeLists.txt`; [doc] wiki ("OpenSSH's SFTP server is also installed"); listagem do zip |
| scp / exec | `scp` vem empacotado (há um hack que reescreve `scp …` para `/mnt/us/koreader/scp …`). `exec` de comandos funciona (shell busybox do Kindle). Fallback documentado: `cat f \| ssh -T -x -p 2222 root@ip 'cat > dst'` | [código] KOB `dbscp-path.patch`; [doc] wiki SSH (Troubleshooting) |
| IP no popup | Ao iniciar, o plugin mostra "SSH port: N" mais `Device:retrieveNetworkInfo()`, que inclui o IPv4 | [código] `main.lua`, `frontend/device/generic/device.lua` |

### Wi-Fi, sleep e suspend
- No Kindle quem decide o suspend é o `powerd` nativo. O KOReader só reage aos eventos (`intoScreenSaver`/`readyToSuspend`/`wakeupFromSuspend`) [código] `frontend/device/kindle/device.lua`.
- O plugin **Keep alive** (More tools → Keep alive) tem a descrição oficial *"Keeps the device awake to prevent automatic Wi-Fi disconnects"* e no Kindle executa `lipc-set-prop com.lab126.powerd preventScreenSaver 1`. Ou seja, **dormir derruba o Wi-Fi** [código] `plugins/keepalive.koplugin/{_meta.lua,main.lua}`. A propriedade `preventScreenSaver` (rw) também aparece em [kindlemodding com.lab126.powerd](https://kindlemodding.org/kindle-apps-and-services/com.lab126.powerd.html) [doc].
- Consequência [inferido]: durante o suspend o processo dropbear fica congelado ou sem rede, então as conexões TCP caem ou dão timeout. Depois de acordar, o Wi-Fi só volta sozinho se "Restore Wi-Fi connection on resume" (`auto_restore_wifi`) estiver ligado, opção que o Kindle suporta (`hasWifiRestore = yes`) [código] `frontend/ui/network/manager.lua`, `device/kindle/device.lua`.
- Ideia de design [inferido]: com a sessão SSH aberta, o app pode rodar `lipc-set-prop com.lab126.powerd preventScreenSaver 1` e reverter para `0` ao terminar, o mesmo mecanismo do Keep alive. Se o app cair, o valor fica preso em 1 e drena bateria, então precisa de cleanup.
- "Disable Wi-Fi connection when inactive" (`auto_disable_wifi`) tem aviso explícito de que não funciona bem no Kindle [código] `manager.lua`.

### Bugs/issues abertas relevantes
- **[#15810](https://github.com/koreader/koreader/issues/15810) (ABERTA, v2026.07.1, Kindle PW4):** o dropbear não sobe com "Address family not supported by protocol", porque tenta bind em IPv6 num kernel sem IPv6. O workaround é `-p 0.0.0.0:2222`. O código do master ainda passa `-p <porta>`. **Risco real: em alguns Kindles o SSH do KOReader pode simplesmente não subir.**
- [#15867](https://github.com/koreader/koreader/issues/15867) (fechada): o stop sem conexões não matava o dropbear (regressão da v2026.07.1).
- [#13420](https://github.com/koreader/koreader/issues/13420): travamento depois de enviar livros por SFTP com o HTTP Inspector em autostart. Um mantenedor comenta que usa usbnetlite para SFTP.

## 2. Alternativas: USBNetwork / usbnetlite / KUAL

| | SSH do KOReader | usbnetlite (notmarek) / USBNetwork (NiLuJe) |
|---|---|---|
| Porta | 2222 | **22** (`PORT="22"` em `/mnt/us/usbnetlite/etc/config`) [código] |
| Sobe quando | O usuário liga no menu, ou com autostart quando o KOReader abre; ou pelo KUAL | Job do **upstart** `start on started volumd`. Com o arquivo-gatilho `/mnt/us/usbnetlite/auto` ("Enable SSH at boot" no KUAL) **sobe no boot, independente do KOReader** [código] `extension/usbnetlite.conf`, `menu.json` |
| Auth | Chave em `koreader/settings/SSH/authorized_keys` ou `-n` | `root` / senha `kindle` (padrão, configurável: `PASSWORD`, `ALLOW_PASSWORD_LOGIN`); chave em `/mnt/us/usbnetlite/etc/dropbear/authorized_keys` [doc] README; [código] `dropbear_be_cool.patch` |
| SFTP | Sim | Sim (compila o `sftp-server` do openssh) [código] `Makefile`, `package.sh` |
| Wi-Fi | Sempre | `USE_WIFI` (o config padrão diz `true`, mas o menu KUAL marca "Block SSH over WiFi (Default)", **inconsistente**) [código] |
| Prós | Já vem com o KOReader, sem instalar nada | Independe do KOReader, sobe no boot, porta 22 padrão. Na issue #13420 um usuário avançado relata uso estável |
| Contras | Depende do KOReader ter sido aberto (ou do KUAL); bug IPv6 #15810 | Exige instalar `update.bin` via MRPI/KUAL; mais uma peça para o usuário configurar |

- USBNetwork clássico (NiLuJe): config em `/mnt/us/usbnet/etc/config`, chaves em `usbnet/etc/authorized_keys`, Wi-Fi via `K3_WIFI="true"`/`USE_WIFI` [doc] [MobileRead wiki USBNetwork](https://wiki.mobileread.com/wiki/USBNetwork), [fórum](https://www.mobileread.com/forums/showthread.php?p=1173614). Os firmwares hard-float (≥5.16.3) quebraram muitas extensões antigas [doc] [kindlemodding FAQ](https://kindlemodding.org/jailbreaking/jailbreak-faq.html). Para Kindle moderno a opção mantida é o usbnetlite.
- "Hotfix" **não é** servidor SSH. É o pacote OTA que mantém o jailbreak vivo (`/var/local/kmc`, `/var/local/mkk`) [doc] [kindlemodding Hotfix](https://kindlemodding.org/kindle-hacking/hotfix.html). Não existe um "SSH do sistema na porta 22" nativo: a porta 22 vem de USBNetwork/usbnetlite.
- **Aparelhos "rootless"** (Scribe Colorsoft, Scribe 3, Kindle 2026/KT7): têm dm-verity, o jailbreak precisa ser reinicializado **a cada reboot** e o suporte a KOReader é "experimental" [doc] [kindlemodding Rootless](https://kindlemodding.org/jailbreaking/whats-next/rootless/), [Getting KOReader](https://kindlemodding.org/jailbreaking/whats-next/getting-koreader/). [NÃO CONFIRMADO] se o SSH ou o `iptables` do plugin funcionam nesses modelos.

**Recomendação de design:** aceitar host e porta configuráveis (2222 por padrão, 22 como alternativa), usuário `root`, auth por chave, e tolerar as duas variantes.

## 3. Sistema de arquivos e metadados do KOReader

Diretório de dados do KOReader no Kindle: `DataStorage:getDataDir()` retorna `"."`, ou seja, o cwd **`/mnt/us/koreader`** [código] `datastorage.lua`.

| Arquivo | Caminho (Kindle) | Formato | Fonte |
|---|---|---|---|
| Settings globais | `/mnt/us/koreader/settings.reader.lua` | tabela Lua (`return {…}`) | [código] `reader.lua` |
| Home folder | chave `home_dir` em settings.reader.lua; **padrão `Device.home_dir = "/mnt/us"`** (não `/mnt/us/documents`) | — | [código] `device/kindle/device.lua`, `filemanagerutil.lua` |
| Histórico | `/mnt/us/koreader/history.lua` (lista `{time=, file=}`; `history_size` padrão 500) | Lua | [código] `frontend/readhistory.lua` |
| Último livro | chave `lastfile` em settings.reader.lua | — | [código] `readhistory.lua` |
| Coleções/Favoritos | `/mnt/us/koreader/settings/collection.lua`. Mapa `nome → { settings={…}, {file=<realpath>, order=n}, … }`; coleção padrão `"favorites"` | Lua | [código] `frontend/readcollection.lua` |
| Capas/metadados (CoverBrowser) | `/mnt/us/koreader/settings/bookinfo_cache.sqlite3` | SQLite (ver §6) | [código] `plugins/coverbrowser.koplugin/bookinfomanager.lua` |
| Estatísticas | `/mnt/us/koreader/settings/statistics.sqlite3` (livro identificado por `md5`) | SQLite | [código] `plugins/statistics.koplugin/main.lua` |
| Host keys + authorized_keys SSH | `/mnt/us/koreader/settings/SSH/` | — | §1 |

### Sidecar `.sdr` (progresso, highlights, bookmarks)
- Arquivo `metadata.<ext>.lua` (ex.: `metadata.epub.lua`) dentro de um diretório `<caminho-do-livro-sem-última-extensão>.sdr`. Chaves relevantes: `percent_finished`, `last_xpointer` (reflow) / `last_page` (PDF), `annotations`, `summary` (status), `doc_props`, `partial_md5_checksum`, `doc_path` [código] `frontend/docsettings.lua`, `readerrolling.lua`, `readerpaging.lua`, `readerannotation.lua`.
- **Opção "Book metadata location"** em Settings → Document. Grava a chave `document_metadata_folder` [código] `common_settings_menu_table.lua`, `docsettings.lua`:
  - `doc` (**padrão**): `/mnt/us/documents/Livro.sdr/` ao lado do livro.
  - `dir`: `/mnt/us/koreader/docsettings/<caminho completo>.sdr/`, espelhando o caminho absoluto.
  - `hash`: `/mnt/us/koreader/hashdocsettings/<2 primeiros hex>/<partialMD5>.sdr/`. O partial MD5 amostra blocos de 1024 bytes em offsets crescentes por potência de 4 (`util.partialMD5`).
- Na leitura o KOReader procura nas três localizações, na ordem de preferência (`getOrderedLocationCandidates`).

### O que acontece ao mover/renomear/apagar **pelo app** (fora do KOReader)
O próprio texto de ajuda do KOReader diz [código] `common_settings_menu_table.lua`:
- **`doc`**: o sdr fica ao lado, então **o app precisa mover/renomear o `.sdr` junto** (`X.epub` → `X.sdr`, e o nome dentro, `metadata.epub.lua`, depende só da extensão). Renomear a **pasta pai** preserva tudo.
- **`dir`**: qualquer move/rename, de arquivo ou pasta, **perde o vínculo** com o metadado. O app teria que mover `docsettings/<path>.sdr` também.
- **`hash`**: sobrevive a move/rename/cópia, mas qualquer modificação do arquivo muda o hash.
- O FileManager do próprio KOReader faz isto ao mover/renomear: `DocSettings.updateLocation` (regrava o metadata no destino e faz purge na origem), `ReadHistory:updateItem` e `ReadCollection:updateItem` [código] `frontend/apps/filemanager/filemanager.lua`. **O app precisa replicar isso**:
  1. mover o `.sdr` (no modo doc);
  2. reescrever `doc_path` dentro do `metadata.*.lua`. [inferido] É opcional para o funcionamento, porque a busca é por caminho, mas mantém consistência;
  3. reescrever `file` em `history.lua` e em `settings/collection.lua`.
- Se não reescrever: as **coleções descartam silenciosamente** entradas cujo arquivo não existe (`buildEntry` → "exclude deleted files") e regravam o arquivo. O **histórico** mantém a entrada "esmaecida" (`dim`), ou a remove se `autoremove_deleted_items_from_history` estiver ligado [código] `readcollection.lua`, `readhistory.lua`.
- Apagar pelo KOReader remove o sdr e opcionalmente arquiva em `.arc` (chave `document_metadata_arc_on_deletion`) [código] `docsettings.lua`. Para o app: apagar livro ⇒ apagar `X.sdr`. Atenção: no modo `doc` o `.sdr` também guarda dados do leitor **nativo** do Kindle (o padrão `.sdr` é da Amazon) [doc] [kindlemodding Extractors](https://kindlemodding.org/kindle-apps-and-services/com.lab126.scanner/extractors.html).
- **Concorrência** [inferido do código]: o KOReader mantém histórico e coleções em memória e só relê o arquivo se o mtime for maior que o da última leitura. O livro aberto tem o sidecar regravado no fechamento, no suspend e no autosave. Se o app mover o livro **aberto**, o KOReader regrava o `.sdr` no caminho antigo. Mitigação: avisar o usuário, ou detectar o `lastfile`.

## 4. Biblioteca nativa do Kindle

- **Indexação:** o `com.lab126.scanner` "watches the userstore filesystem at /mnt/us/ and indexes books". Em add/delete ele dispara eventos `SCANNER_ADD`/`SCANNER_DELETE` para os extractors. As propriedades LIPC `doFullScan` e `reScanFile` permitem forçar varredura [doc] [kindlemodding scanner](https://kindlemodding.org/kindle-apps-and-services/com.lab126.scanner/index.html). Portanto um arquivo escrito via SFTP em `/mnt/us/documents` **deve** aparecer na biblioteca nativa. Se não aparecer, dá para usar `lipc-set-prop com.lab126.scanner doFullScan 1` [NÃO CONFIRMADO o valor exato e o comportamento ao vivo].
- O indexador só cataloga formatos com extractor registrado no `appreg.db` (EPUB não é nativo). Pastas dentro de `documents/`: [NÃO CONFIRMADO] em fonte primária como a UI nativa as mostra.
- **Coleções nativas:** `/var/local/cc.db` (SQLite, "Content Catalog"). Ferramentas antigas exportavam e importavam via sqlite e **exigiam reboot**. Há relatos de "all my folders are invisible" e de livros faltando quando o import acontece com a CPU ocupada, e o sistema mantém 5 cópias de backup [doc] [MobileRead (2012)](https://www.mobileread.com/forums/showthread.php?p=2021526). Schema em firmware moderno: [NÃO CONFIRMADO]. **Recomendação: não editar o cc.db no MVP**, porque é de alto risco e versionado pelo firmware.
- **Formatos:**
  - KOReader (crengine): epub, epub3, fb2, fb3, mobi, azw (mobi8 **sem DRM**), doc, docx, odt, rtf, chm, pdb, prc, html/htm/xhtml, txt, md, mais variantes `.zip` [código] `frontend/document/credocument.lua`.
  - KOReader (MuPDF): pdf, xps, cbz, cbt, cbr, imagens, svg [código] `pdfdocument.lua`.
  - KOReader (DjVuLibre): djvu [código] `djvudocument.lua`.
  - O KOReader **não lê KFX nem livros com DRM da Amazon**; o suporte a AZW3 é limitado [doc] [kindlemodding FAQ](https://kindlemodding.org/jailbreaking/jailbreak-faq.html).
  - Kindle nativo via USB: KFX, AZW3, AZW, MOBI, PRC, PDF, TXT. **EPUB não** (só via Send-to-Kindle, que converte). Fonte: página de ajuda da Amazon `nodeId=TCUBEdEkbIhK07ysFu`, que **retornou 503 quando tentei abrir**. A lista veio do snippet da busca [NÃO CONFIRMADO diretamente].
  - Conclusão de design: EPUB enviado é útil só no KOReader. Vale um filtro/aviso de "lê só no KOReader".

## 5. Descoberta na rede

- **IP no KOReader:** Settings → Network → **Network info** (`network_info`). Mostra interface, MAC, SSID, IPv4, gateway e ping do gateway. A mesma informação aparece no popup ao iniciar o SSH. Também existe a ação de gesto `show_network_info` [código] `frontend/ui/network/manager.lua`, `device/generic/device.lua`, `dispatcher.lua`.
- **DHCP:** o KOReader não oferece IP estático na UI (issue aberta [#2987](https://github.com/koreader/koreader/issues/2987) "Can I set up koreader's wifi to use a static IP"). O IP vem do DHCP do roteador e **pode mudar**. Mitigação: reserva DHCP no roteador. [NÃO CONFIRMADO] qual hostname o Kindle anuncia via DHCP.
- **mDNS:** nenhuma referência a avahi/mdns/zeroconf no código do KOReader nem do Kindle (`grep` vazio) [código]. Assumir que **não há mDNS**.
- Estratégia [inferido]: (a) lembrar o último IP e o fingerprint da host key; (b) se falhar, varrer a sub-rede local (/24) na porta 2222/22 procurando o banner `SSH-2.0-dropbear_* (KOReader)` [código: `IDENT_VERSION_PART`]; (c) confirmar pelo fingerprint da host key, que é estável graças ao `-R` e às chaves em `settings/SSH/`.

## 6. Capas e metadados (CoverBrowser)

- DB: `/mnt/us/koreader/settings/bookinfo_cache.sqlite3`, `user_version = 20201210` [código] `bookinfomanager.lua`.
- Tabela `bookinfo`: `bcid`, `directory`, `filename` (índice único `(directory, filename)`), `filesize`, `filemtime`, `in_progress` (0 = pronto), `unsupported`, `cover_fetched`, `has_meta`, `has_cover`, `cover_sizetag` (tamanho original "WxH"), `ignore_meta`, `ignore_cover`, `pages`, `title`, `authors`, `series`, `series_index`, `language`, `keywords`, `description`, `cover_w`, `cover_h`, `cover_bb_type`, `cover_bb_stride`, `cover_bb_data`. Tabela `config(key,value)`.
- **A capa não é JPEG/PNG.** `cover_bb_data` é o **buffer bruto do Blitbuffer comprimido com zstd**. Para decodificar: `zstd_decompress` → `stride*h` bytes. `cover_bb_type` segue o `BB.TYPE_*`: 0=BB4, 1=BB8 (gray 8bpp), 2=BB8A, 3=RGB16, 4=RGB24, 5=RGB32. Na prática o Kindle grayscale usa BB8 [inferido] [código] `bookinfomanager.lua` linhas ~540-550, KOB `ffi/blitbuffer.lua`. A miniatura vem **reduzida** ao tamanho máximo de exibição do mosaico.
- Só existem linhas para livros que o usuário já viu no File Browser com CoverBrowser em modo mosaico/lista detalhada [inferido].
- **Journal mode:** WAL se `Device:canUseWAL()`. Só Kindle2 e DXG estão com `no`, então **Kindles modernos usam WAL** [código] `device/kindle/device.lua`. Para ler offline no Mac é preciso **baixar `.sqlite3`, `-wal` e `-shm` juntos**, num snapshot coerente, senão perde as linhas recentes. [NÃO CONFIRMADO] se o Kindle tem binário `sqlite3` para fazer checkpoint remoto.
- Viável na web: sim, com decodificação zstd + raw gray → PNG no backend local. Plano B: extrair a capa do próprio EPUB/PDF no Mac.

## 7. Riscos

- **Sistema de arquivos:** `/mnt/us` é um overlay **FUSE ("fuse.fsp")** sobre o **vfat** real em `/mnt/base-us`, montado `noexec` [código] `platform/kindle/koreader.sh` (comentários). O KOReader trata `vfat` e `fuse.fsp` com regras de FAT [código] `util.getSafeFilename`.
  - Caracteres proibidos: `\ / : * ? " < > |`.
  - Nome limitado a 255 caracteres UCS-2; o KOReader usa margem de 240.
  - Remove `\r`, `\n` e `\t`.
  - Limite de arquivo de 4 GiB (FAT32), sem permissões e sem symlinks: [NÃO CONFIRMADO] especificamente para o fsp.
  - Case-insensitivity: [NÃO CONFIRMADO] para o fsp, mas é o comportamento do vfat. Tratar rename que só muda maiúsculas/minúsculas como operação de dois passos.
- **Espaço:** só o userstore (`/mnt/us`) importa. Checar com `df /mnt/us` via exec antes do upload. [NÃO CONFIRMADO] se o `df` do busybox reporta corretamente no fsp.
- **Sync/Amazon:** um aparelho muito tempo em modo avião pode ter livros sideloaded apagados ao reconectar [doc] kindlemodding FAQ (relato).
- **Desregistrar** o Kindle apaga a pasta `documents` [doc] kindlemodding FAQ.
- **Firmware/OTA:** atualização pode remover o jailbreak. Hard-float (≥5.16.3) quebra extensões soft-float. Rootless exige re-jailbreak a cada boot [doc] kindlemodding FAQ/Rootless.
- **SSH do KOReader:** bug IPv6 #15810 (pode não subir); o servidor não volta sozinho depois de reboot do Kindle sem abrir o KOReader; o Wi-Fi cai no sleep.
- **Escrita concorrente** com o KOReader aberto (histórico, coleções, sdr do livro aberto): §3.
- **Scanner nativo:** gravações grandes disparam indexação; na [#13420](https://github.com/koreader/koreader/issues/13420) houve lentidão e crash (causa não fechada). Uploads devem ser atômicos: gravar `.part` e depois `rename`. [inferido]
