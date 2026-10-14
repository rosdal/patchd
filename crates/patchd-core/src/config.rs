//! `/etc/patchd/config.toml`: small, typed, and validated. Every key is optional.
//!
//! The doc comments on these structs are the reference: `cargo xtask schema` turns them
//! into `docs/config.md`, so the docs cannot drift from the code.

use std::path::{Path, PathBuf};

use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

use crate::plan::{Exclusions, Filter};

/// Where the config file lives unless `--config` says otherwise.
pub const DEFAULT_PATH: &str = "/etc/patchd/config.toml";

/// Environment variables that override the file: `PATCHD_<SECTION>_<KEY>`, such as
/// `PATCHD_APPLY_FILTER=all`.
const ENV_SECTIONS: [&str; 3] = ["apply", "reboot", "log"];

const LOG_LEVELS: [&str; 5] = ["error", "warn", "info", "debug", "trace"];

/// The Patchd configuration.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(default, deny_unknown_fields)]
pub struct Config {
    pub apply: ApplyConfig,
    pub reboot: RebootConfig,
    pub log: LogConfig,
}

/// What `patchd apply` installs, and how careful it is.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(default, deny_unknown_fields)]
pub struct ApplyConfig {
    /// `security` installs only security updates (plus the new packages they need);
    /// `all` installs every pending update.
    pub filter: Filter,
    /// Refresh the package lists (`apt-get update`, `dnf makecache`) before planning.
    pub refresh: bool,
    /// Allow a run that removes packages. When false, a plan that would remove anything
    /// aborts before any package is touched.
    pub allow_removals: bool,
    /// When `dpkg --audit` reports half-configured packages, run `dpkg --configure -a`
    /// first instead of aborting.
    pub repair: bool,
    /// Package name globs that are never installed, such as `["pve-kernel-*",
    /// "docker-ce*"]`. Matching updates are listed as skipped.
    pub exclude: Vec<String>,
}

impl Default for ApplyConfig {
    fn default() -> Self {
        Self {
            filter: Filter::Security,
            refresh: true,
            allow_removals: false,
            repair: false,
            exclude: Vec::new(),
        }
    }
}

/// What happens when an update needs a reboot.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(default, deny_unknown_fields)]
pub struct RebootConfig {
    /// `never` only reports that a reboot is needed; `if-required` reboots after a
    /// successful run that needs one.
    pub mode: RebootMode,
    /// Minutes between the end of the run and the reboot (`shutdown -r +N`), so logged-in
    /// users get a warning and can cancel it with `shutdown -c`.
    pub delay_minutes: u32,
}

impl Default for RebootConfig {
    fn default() -> Self {
        Self {
            mode: RebootMode::Never,
            delay_minutes: 5,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum RebootMode {
    Never,
    IfRequired,
}

/// Run logs and log output.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(default, deny_unknown_fields)]
pub struct LogConfig {
    /// Delete run logs older than this many days. The last 20 runs are always kept.
    pub retention_days: u32,
    /// `error`, `warn`, `info`, `debug` or `trace`. `RUST_LOG` overrides it.
    pub level: String,
}

impl Default for LogConfig {
    fn default() -> Self {
        Self {
            retention_days: 90,
            level: "info".to_owned(),
        }
    }
}

#[derive(Debug, thiserror::Error)]
pub enum ConfigError {
    #[error("{path}: {source}")]
    Read {
        path: PathBuf,
        source: std::io::Error,
    },
    /// The toml crate's message already names the line and column.
    #[error("{origin}: {message}")]
    Parse { origin: String, message: String },
    #[error("{origin}: {}", .problems.join("; "))]
    Invalid {
        origin: String,
        problems: Vec<String>,
    },
}

impl Config {
    /// Reads the config file, then applies `PATCHD_*` overrides from `env`.
    ///
    /// A missing file at the default path means defaults; a missing file named with
    /// `--config` is an error, because the caller asked for that file.
    ///
    /// # Errors
    ///
    /// When the file cannot be read, does not parse, has unknown keys, or fails
    /// validation, and when an environment override is malformed.
    pub fn load(
        path: Option<&Path>,
        env: impl IntoIterator<Item = (String, String)>,
    ) -> Result<Self, ConfigError> {
        let (path, explicit) = match path {
            Some(p) => (p.to_path_buf(), true),
            None => (PathBuf::from(DEFAULT_PATH), false),
        };
        let text = match std::fs::read_to_string(&path) {
            Ok(text) => text,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound && !explicit => String::new(),
            Err(source) => return Err(ConfigError::Read { path, source }),
        };
        let origin = path.display().to_string();
        // Parse the file on its own first, so errors point at its lines.
        Self::parse(&text, &origin)?;

        let mut table: toml::Table = text.parse().map_err(|e| parse_error(&origin, &e))?;
        let overrides = env_overrides(env)?;
        let from_env = !overrides.is_empty();
        for (section, key, value) in overrides {
            let entry = table
                .entry(section)
                .or_insert_with(|| toml::Value::Table(toml::Table::new()));
            if let toml::Value::Table(t) = entry {
                t.insert(key, value);
            }
        }
        let origin = if from_env {
            format!("{origin} with PATCHD_* overrides")
        } else {
            origin
        };
        let config: Self = toml::Value::Table(table)
            .try_into()
            .map_err(|e: toml::de::Error| parse_error(&origin, &e))?;
        config.validate(&origin)?;
        Ok(config)
    }

    /// Parses and validates config text. `origin` names it in errors.
    ///
    /// # Errors
    ///
    /// When the text does not parse, has unknown keys, or fails validation.
    pub fn parse(text: &str, origin: &str) -> Result<Self, ConfigError> {
        let config: Self = toml::from_str(text).map_err(|e| parse_error(origin, &e))?;
        config.validate(origin)?;
        Ok(config)
    }

    fn validate(&self, origin: &str) -> Result<(), ConfigError> {
        let mut problems = Vec::new();
        if let Err(e) = Exclusions::new(&self.apply.exclude) {
            problems.push(format!("apply.exclude: {e}"));
        }
        if self.reboot.delay_minutes > 24 * 60 {
            problems.push(format!(
                "reboot.delay_minutes: {} is more than a day",
                self.reboot.delay_minutes
            ));
        }
        if self.log.retention_days == 0 {
            problems.push("log.retention_days: must be at least 1".to_owned());
        }
        if !LOG_LEVELS.contains(&self.log.level.as_str()) {
            problems.push(format!(
                "log.level: {:?} is not one of {}",
                self.log.level,
                LOG_LEVELS.join(", ")
            ));
        }
        if problems.is_empty() {
            Ok(())
        } else {
            Err(ConfigError::Invalid {
                origin: origin.to_owned(),
                problems,
            })
        }
    }

    /// The effective config as TOML, for `patchd config show`.
    ///
    /// # Panics
    ///
    /// Never: every field serializes.
    #[must_use]
    pub fn to_toml(&self) -> String {
        toml::to_string(self).expect("config serializes")
    }

    /// The compiled `apply.exclude` globs.
    ///
    /// # Panics
    ///
    /// Never for a config from [`Config::load`] or [`Config::parse`], which validate
    /// the globs.
    #[must_use]
    pub fn exclusions(&self) -> Exclusions {
        Exclusions::new(&self.apply.exclude).expect("exclusions validated on load")
    }
}

fn parse_error(origin: &str, e: &toml::de::Error) -> ConfigError {
    ConfigError::Parse {
        origin: origin.to_owned(),
        message: e.to_string().trim_end().to_owned(),
    }
}

/// `PATCHD_APPLY_ALLOW_REMOVALS=true` → `("apply", "allow_removals", true)`. Values are
/// TOML literals (`true`, `5`, `["a", "b"]`); anything that isn't one is a string.
fn env_overrides(
    env: impl IntoIterator<Item = (String, String)>,
) -> Result<Vec<(String, String, toml::Value)>, ConfigError> {
    let mut out = Vec::new();
    for (name, value) in env {
        let Some(rest) = name.strip_prefix("PATCHD_") else {
            continue;
        };
        let rest = rest.to_ascii_lowercase();
        let Some((section, key)) = ENV_SECTIONS
            .iter()
            .find_map(|s| Some((*s, rest.strip_prefix(s)?.strip_prefix('_')?)))
        else {
            continue;
        };
        let parsed = format!("v = {value}")
            .parse::<toml::Table>()
            .ok()
            .and_then(|mut t| t.remove("v"))
            .unwrap_or_else(|| toml::Value::String(value.clone()));
        if key.is_empty() {
            return Err(ConfigError::Parse {
                origin: name,
                message: "no key after the section".to_owned(),
            });
        }
        out.push((section.to_owned(), key.to_owned(), parsed));
    }
    out.sort_by(|a, b| (&a.0, &a.1).cmp(&(&b.0, &b.1)));
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn no_env() -> Vec<(String, String)> {
        Vec::new()
    }

    #[test]
    fn empty_file_is_all_defaults() {
        let c = Config::parse("", "test").unwrap();
        assert_eq!(c, Config::default());
        assert_eq!(c.apply.filter, Filter::Security);
        assert!(c.apply.refresh);
        assert_eq!(c.reboot.mode, RebootMode::Never);
        assert_eq!(c.log.retention_days, 90);
    }

    #[test]
    fn shipped_example_parses_to_the_defaults() {
        let text = include_str!("../../../packaging/config.toml");
        assert_eq!(Config::parse(text, "example").unwrap(), Config::default());
    }

    #[test]
    fn typo_is_an_error_with_a_line_number() {
        let err = Config::parse("[apply]\nfilter = \"all\"\nallow_removal = true\n", "cfg")
            .unwrap_err()
            .to_string();
        assert!(err.contains("line 3"), "{err}");
        assert!(err.contains("allow_removal"), "{err}");
    }

    #[test]
    fn values_are_validated() {
        let err = Config::parse(
            "[apply]\nexclude = [\"[\"]\n[log]\nretention_days = 0\nlevel = \"loud\"\n",
            "cfg",
        )
        .unwrap_err();
        let ConfigError::Invalid { problems, .. } = err else {
            panic!("{err}")
        };
        assert_eq!(problems.len(), 3, "{problems:?}");
    }

    #[test]
    fn reboot_mode_is_kebab_case() {
        let c = Config::parse("[reboot]\nmode = \"if-required\"\n", "cfg").unwrap();
        assert_eq!(c.reboot.mode, RebootMode::IfRequired);
    }

    #[test]
    fn env_overrides_the_file() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        std::fs::write(&path, "[apply]\nfilter = \"all\"\nrefresh = true\n").unwrap();
        let env = [
            ("PATCHD_APPLY_REFRESH", "false"),
            ("PATCHD_APPLY_EXCLUDE", r#"["pve-kernel-*"]"#),
            ("PATCHD_REBOOT_MODE", "if-required"),
            ("PATCHD_GIT_SHA", "not config"),
            ("HOME", "/root"),
        ]
        .map(|(k, v)| (k.to_owned(), v.to_owned()));
        let c = Config::load(Some(&path), env).unwrap();
        assert_eq!(c.apply.filter, Filter::All);
        assert!(!c.apply.refresh);
        assert_eq!(c.apply.exclude, ["pve-kernel-*"]);
        assert_eq!(c.reboot.mode, RebootMode::IfRequired);
    }

    #[test]
    fn unknown_env_key_is_an_error() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        std::fs::write(&path, "").unwrap();
        let env = [("PATCHD_APPLY_FILTR".to_owned(), "all".to_owned())];
        let err = Config::load(Some(&path), env).unwrap_err().to_string();
        assert!(err.contains("filtr"), "{err}");
    }

    #[test]
    fn missing_explicit_file_is_an_error() {
        let err = Config::load(Some(Path::new("/nonexistent/patchd.toml")), no_env());
        assert!(matches!(err, Err(ConfigError::Read { .. })));
    }

    #[test]
    fn show_round_trips() {
        let mut c = Config::default();
        c.apply.exclude = vec!["docker-ce*".to_owned()];
        assert_eq!(Config::parse(&c.to_toml(), "show").unwrap(), c);
    }
}
