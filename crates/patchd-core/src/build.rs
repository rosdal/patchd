pub const VERSION: &str = env!("CARGO_PKG_VERSION");
pub const GIT_SHA: &str = env!("PATCHD_GIT_SHA");
pub const TARGET: &str = env!("PATCHD_TARGET");
pub const LONG_VERSION: &str = concat!(
    env!("CARGO_PKG_VERSION"),
    " (",
    env!("PATCHD_GIT_SHA"),
    " ",
    env!("PATCHD_TARGET"),
    ")"
);

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn long_version_combines_version_sha_and_target() {
        assert_eq!(LONG_VERSION, format!("{VERSION} ({GIT_SHA} {TARGET})"));
        assert!(!GIT_SHA.is_empty());
        assert!(GIT_SHA.len() <= 10);
        assert!(!TARGET.is_empty());
    }
}
