ci: fmt lint test schema deny openapi

fmt:
    cargo fmt --all --check

lint:
    cargo clippy --workspace --all-targets --locked -- -D warnings

test:
    cargo test --workspace --locked

deny:
    cargo deny check

openapi:
    pnpm --package=@redocly/cli@2.63.0 dlx redocly lint protocol/openapi.yaml

schema:
    cargo xtask schema
    git diff --exit-code protocol/
