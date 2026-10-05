# 4. Discovery: saved address, then LAN scan

The Device gets its address from DHCP and it may change. The Connector first dials
the saved address; if that fails, it scans the local /24 of each active interface on
the SSH port and keeps the first host whose host key matches the pinned one, then
saves the new address. The scan never leaves the local subnet.
