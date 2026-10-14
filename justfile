ci: fmt lint test deny openapi

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
