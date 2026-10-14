//! The filesystem root every system path goes through, so checks that read `/etc`,
//! `/run` or `/lib/modules` can run against a temporary directory in tests.

use std::path::{Path, PathBuf};

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Root(PathBuf);

impl Root {
    /// The real `/`.
    #[must_use]
    pub fn system() -> Self {
        Self(PathBuf::from("/"))
    }

    /// A fake root, such as a test's temporary directory.
    #[must_use]
    pub fn at(path: impl Into<PathBuf>) -> Self {
        Self(path.into())
    }

    /// `path` under this root. Absolute paths are taken relative to the root, so
    /// `root.join("/etc/apt")` stays inside it.
    #[must_use]
    pub fn join(&self, path: impl AsRef<Path>) -> PathBuf {
        let path = path.as_ref();
        self.0.join(path.strip_prefix("/").unwrap_or(path))
    }

    #[must_use]
    pub fn is_system(&self) -> bool {
        self.0 == Path::new("/")
    }
}

impl Default for Root {
    fn default() -> Self {
        Self::system()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn absolute_paths_stay_inside_the_root() {
        let root = Root::at("/tmp/fake");
        assert_eq!(root.join("/etc/apt"), PathBuf::from("/tmp/fake/etc/apt"));
        assert_eq!(root.join("etc/apt"), PathBuf::from("/tmp/fake/etc/apt"));
        assert_eq!(Root::system().join("/etc"), PathBuf::from("/etc"));
    }
}
