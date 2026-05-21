# Security

## Reporting vulnerabilities

Email security@unitsense.ai. We aim to respond within 2 business days.

## Verifying release signatures

Every binary is signed via cosign keyless using GitHub's OIDC.

```bash
VERSION=0.1.0
ARCH=darwin_arm64   # or linux_amd64 / windows_amd64
ARCHIVE=unitsense-agent_${VERSION}_${ARCH}.tar.gz

curl -fsSLO https://github.com/UnitSense/agent/releases/download/v${VERSION}/${ARCHIVE}
curl -fsSLO https://github.com/UnitSense/agent/releases/download/v${VERSION}/${ARCHIVE}.sig
curl -fsSLO https://github.com/UnitSense/agent/releases/download/v${VERSION}/${ARCHIVE}.crt

cosign verify-blob \
  --certificate-identity "https://github.com/UnitSense/agent/.github/workflows/release.yml@refs/tags/v${VERSION}" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --signature ${ARCHIVE}.sig \
  --certificate ${ARCHIVE}.crt \
  ${ARCHIVE}
```

## Privacy contract

The agent ships only aggregate metrics — never prompts, responses, file
contents, or raw tool inputs/outputs. See README.md for the full payload
schema.
