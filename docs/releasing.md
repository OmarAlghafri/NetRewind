# Cutting a release

## The signing key

Checksums prove a download arrived as the server sent it. They do not prove who
built it — anyone who can publish a release can publish checksums for it. A
signature made with a key that never touches CI is what turns the update channel
from "trust whoever holds the repository" into something an operator can reason
about, and it matters most here because the recorder installs its own
replacements.

Once, on a machine you control:

```bash
make signing-key
```

It writes `~/.netrewind/signing.key`, refuses to overwrite an existing one, and
prints the public half in the form the configuration wants:

```yaml
update:
  public_key: "<the 44 characters make signing-key printed>"
```

That value is the trust anchor for every recorder configured with it, so it has
to be the one *your* key printed. A key copied from a document is a key
somebody else holds the private half of, and a recorder configured with it will
either refuse every release you publish or accept one you did not.

**Back the private key up offline, now.** Losing it means every recorder
configured with the matching public key stops accepting updates, and there is no
recovery. It must never be committed and never be put into CI — a key CI can
reach is a key a CI compromise can sign with, which is the entire thing this
protects against.

## Building

```bash
make release VERSION=0.9.0
```

That runs the tests, builds `linux/amd64` and `linux/arm64`, writes
`SHA256SUMS`, signs it, and then **verifies the signature it just made using the
same code the recorder uses to check one**. Signing without verifying is how you
publish a release every updater refuses: openssl's ed25519 needs `-rawin`, and
without it the signature covers a hash of the file rather than the file, which
nothing accepts. Better to find that here than from the field.

With no key present it says so loudly and produces an unsigned release rather
than failing — useful for a test build, and impossible to do by accident.

For the appliance image:

```bash
sudo make image
gzip -9 dist/netrewind-appliance.img
```

## Publishing

Upload from the machine that has the key, because the signature has to be made
there:

- `netrewind-<version>-linux-amd64.tar.gz`
- `netrewind-<version>-linux-arm64.tar.gz`
- `netrewind-<version>-appliance-amd64.img.gz` (optional)
- `SHA256SUMS`
- `SHA256SUMS.sig` ← without this, recorders with a key configured refuse the release

Then tag it:

```bash
git tag -a v0.9.0 -m "NetRewind 0.9.0"
git push origin main v0.9.0
```

> **The CI release job builds unsigned artefacts.** That is deliberate: CI has no
> key, and giving it one would remove the guarantee. If you let CI publish, add
> `SHA256SUMS.sig` to the release yourself afterwards, or cut releases locally.

## What updaters do with it

A recorder with `update.apply` on will, within a day:

1. see the new tag through the releases API
2. download the tarball for its architecture over HTTPS
3. check it against `SHA256SUMS`
4. if `update.public_key` is configured, check `SHA256SUMS.sig` against it —
   and **refuse the release** if there is no valid signature
5. run the new binary before replacing anything
6. keep the old binary, add rules that are new, leave edited rules alone
7. record `system.updated` and restart

Releases before 0.9.0 cannot be installed this way: they predate `netrewindd
--version`, so step 5 cannot verify them and refuses. That costs nothing —
the updater first shipped in 0.9.0 — and it is worth far more than loosening the
one check standing between an update and a recorder that has quietly stopped.

## Before tagging

- `make load` — throughput, run alone so the number means something
- `sudo make lab` — fourteen faults in network namespaces
- The GNS3 topology, for what namespaces cannot produce:
  ```bash
  py lab/gns3/provision_lab.py --start
  py lab/gns3/inject.py gateway-moved
  ```
- `sudo make image` and boot it once in QEMU

CI covers the first two on every push. The last two need hardware and are a
release gate rather than a per-commit check.
