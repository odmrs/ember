# 3. Key-only auth, pinned host key

At Pairing, ember generates its own ed25519 key pair in the Profile (private key
file mode 0600) and installs the public key on the Device. From then on ember never
stores or sends a password. The Device host key fingerprint is recorded at Pairing
and every later connection must match it, which also makes network scanning safe:
a host on the LAN that answers on the port but has a different key is ignored.
