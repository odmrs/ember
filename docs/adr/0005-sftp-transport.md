# 5. SFTP as the transport

KOReader ships OpenSSH's `sftp-server`, so every file operation goes through SFTP
using `github.com/pkg/sftp` (plus its dependency `github.com/kr/fs`), pinned and
vendored. The alternative, driving `ls`/`mv`/`cat` through a remote shell, needs no
extra modules but puts every user-supplied file name one quoting mistake away from
command execution as root on the Device. Two frozen modules are the cheaper risk.
This amends ADR-0002's dependency list.
