# Installing Patchd on Linux

Patchd ships as a `.deb` and an `.rpm` per architecture (`amd64`/`x86_64` and `arm64`/`aarch64`). The binary is statically linked, so one package works on every supported release: Debian 12 and 13, Ubuntu 22.04 and newer, Proxmox VE, Raspberry Pi OS, Fedora, and RHEL, AlmaLinux and Rocky Linux 9 and 10.

Installing the package changes nothing on the system by itself. It installs the `patchd` binary, a commented `/etc/patchd/config.toml`, and two systemd units, `patchd-apply.service` and `patchd-apply.timer`, **neither of them enabled**. Patchd starts installing updates only once you enable the timer.

## 1. Install the package

Download the package and `SHA256SUMS` from the [latest release](https://github.com/rosdal/patchd/releases), check it, and install it:

```sh
sha256sum --check --ignore-missing SHA256SUMS

# Debian, Ubuntu, Proxmox VE, Raspberry Pi OS
sudo apt-get install ./patchd_*_amd64.deb

# Fedora, RHEL, AlmaLinux, Rocky Linux
sudo dnf install ./patchd-*.x86_64.rpm
```

## 2. Look before you leap

Both of these are read-only:

```sh
patchd scan          # everything pending
patchd plan          # exactly what a run would install, under the configured filter
```

## 3. Configure

Edit `/etc/patchd/config.toml` ([every key](config.md)), then check it:

```sh
sudo patchd config check
```

The defaults are the cautious ones: security updates only, no removals, no reboots. To keep a package away from Patchd altogether, add it to `exclude`; to pin it for every tool, `apt-mark hold` it.

Package upgrades keep your edited `config.toml`: it is a conffile in the `.deb` and `%config(noreplace)` in the `.rpm`.

## 4. Turn off unattended-upgrades (Debian and Ubuntu)

Patchd replaces unattended-upgrades; running both makes them compete for the dpkg lock at night ([ADR 0010](adr/0010-replace-unattended-upgrades.md)). Patchd warns about it on every run until it is off:

```sh
sudo systemctl disable --now apt-daily-upgrade.timer
echo 'APT::Periodic::Unattended-Upgrade "0";' | sudo tee /etc/apt/apt.conf.d/99patchd
```

Leave `apt-daily.timer` alone: it only refreshes the package lists, which keeps `patchd scan` current.

## 5. Try one run by hand

```sh
sudo patchd apply
```

It asks for confirmation, then prints what it installed and where the run log is. `sudo patchd history` lists runs, and `sudo patchd history <run-id>` shows one step by step.

## 6. Enable the nightly run

```sh
sudo systemctl enable --now patchd-apply.timer
systemctl list-timers patchd-apply.timer
```

The timer fires at 03:30 plus up to 30 minutes of random delay, and catches up after downtime. To change the time on one host, add a drop-in instead of editing the unit; the empty `OnCalendar=` clears the default first:

```sh
sudo mkdir -p /etc/systemd/system/patchd-apply.timer.d
printf '[Timer]\nOnCalendar=\nOnCalendar=*-*-* 02:30\n' |
  sudo tee /etc/systemd/system/patchd-apply.timer.d/schedule.conf
sudo systemctl daemon-reload
```

Each morning, `patchd status` shows the last run, the next one, and whether a reboot is pending. The service's output is also in the journal: `journalctl -u patchd-apply`.

## Reboots

By default Patchd only reports that a reboot is needed (exit code 20, `patchd status`, the run log) and says why. With `[reboot] mode = "if-required"`, a successful run that needs one reboots the host 5 minutes later; logged-in users get a warning and can cancel it with `sudo shutdown -c`. See [ADR 0011](adr/0011-reboot-detect-dont-act.md).

## Removing Patchd

```sh
sudo apt-get remove patchd      # or: sudo dnf remove patchd
```

Removal stops the timer; a run already in progress finishes first. Run logs in `/var/log/patchd` stay until you purge the package (`apt-get purge patchd`) or delete them.
