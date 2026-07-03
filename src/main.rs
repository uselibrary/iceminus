use anyhow::{Context, Result, bail};
use clap::Parser;
use std::{
    collections::{BTreeMap, HashSet},
    ffi::OsStr,
    fs,
    io::{self, BufRead, BufReader, BufWriter, Write},
    path::{Path, PathBuf},
};
use tempfile::NamedTempFile;
use walkdir::WalkDir;

const EMBEDDED_SENTINEL: &str = "@embedded";
const EMBEDDED_SENSITIVE: &str = include_str!("../sensitive_words.txt");

#[derive(Debug, Parser)]
#[command(name = "iceminus")]
#[command(about = "Scan YAML files and comment out lines containing sensitive words.")]
struct Cli {
    /// Directory or file path to scan for YAML files.
    ///
    /// On Windows, if omitted, defaults to:
    /// %APPDATA%\Rime\cn_dicts
    #[arg(long)]
    path: Option<PathBuf>,

    /// Print what would be changed without modifying files.
    #[arg(long)]
    dry_run: bool,

    /// Path to sensitive words file, or @embedded to use embedded list.
    #[arg(long, default_value = EMBEDDED_SENTINEL)]
    sensitive: String,
}

#[derive(Debug, Default)]
struct ProcStats {
    files_scanned: usize,
    files_with_matches: usize,
    total_matched_lines: usize,
    ops_per_file: BTreeMap<PathBuf, usize>,
}

fn main() {
    if let Err(err) = run() {
        eprintln!("error: {err:#}");
        std::process::exit(1);
    }
}

fn run() -> Result<()> {
    let cli = Cli::parse();

    let path = match cli.path {
        Some(path) => path,
        None => default_scan_path().context(
            "missing --path; usage: iceminus --path <path> [--dry-run] [--sensitive <file|@embedded>]",
        )?,
    };

    let words = load_sensitive(&cli.sensitive)
        .with_context(|| format!("failed to load sensitive words from {}", cli.sensitive))?;

    if words.is_empty() {
        println!("no sensitive words found; nothing to do");
        return Ok(());
    }

    let folder_path = scanned_folder_path(&path);
    let mut stats = ProcStats::default();

    let stdout = io::stdout();
    let mut out = BufWriter::new(stdout.lock());

    process_path(&path, &words, cli.dry_run, &mut stats, &mut out)?;
    out.flush()?;

    print_summary(&folder_path, &stats);

    Ok(())
}

fn default_scan_path() -> Option<PathBuf> {
    #[cfg(windows)]
    {
        std::env::var_os("APPDATA")
            .map(PathBuf::from)
            .map(|p| p.join("Rime").join("cn_dicts"))
    }

    #[cfg(not(windows))]
    {
        None
    }
}

fn scanned_folder_path(path: &Path) -> PathBuf {
    let abs = path
        .canonicalize()
        .or_else(|_| {
            if path.is_absolute() {
                Ok(path.to_path_buf())
            } else {
                std::env::current_dir().map(|cwd| cwd.join(path))
            }
        })
        .unwrap_or_else(|_| path.to_path_buf());

    match fs::metadata(path) {
        Ok(meta) if meta.is_file() => abs.parent().unwrap_or(&abs).to_path_buf(),
        _ => abs,
    }
}

fn load_sensitive(path: &str) -> Result<Vec<String>> {
    let source = match path.trim() {
        "" | EMBEDDED_SENTINEL => EMBEDDED_SENSITIVE.to_owned(),
        file_path => fs::read_to_string(file_path)
            .with_context(|| format!("cannot read sensitive words file: {file_path}"))?,
    };

    let mut seen = HashSet::new();
    let mut words = Vec::new();

    for line in source.lines() {
        let word = line.trim();

        if word.is_empty() {
            continue;
        }

        // 改进点：敏感词文件支持注释。
        // 如果你确实需要把以 # 开头的内容作为敏感词，可以删除这个判断。
        if word.starts_with('#') {
            continue;
        }

        if seen.insert(word.to_owned()) {
            words.push(word.to_owned());
        }
    }

    Ok(words)
}

fn process_path(
    root: &Path,
    words: &[String],
    dry_run: bool,
    stats: &mut ProcStats,
    out: &mut dyn Write,
) -> Result<()> {
    let metadata =
        fs::metadata(root).with_context(|| format!("cannot access path: {}", root.display()))?;

    if metadata.is_dir() {
        for entry in WalkDir::new(root).follow_links(false) {
            let entry =
                entry.with_context(|| format!("failed walking under {}", root.display()))?;

            if entry.file_type().is_dir() {
                continue;
            }

            let path = entry.path();

            if is_yaml_file(path) {
                process_one_yaml(path, words, dry_run, stats, out)?;
            }
        }

        return Ok(());
    }

    if !metadata.is_file() {
        bail!(
            "path is neither a directory nor a regular file: {}",
            root.display()
        );
    }

    process_one_yaml(root, words, dry_run, stats, out)
}

fn process_one_yaml(
    path: &Path,
    words: &[String],
    dry_run: bool,
    stats: &mut ProcStats,
    out: &mut dyn Write,
) -> Result<()> {
    stats.files_scanned += 1;

    let matched_lines = process_file(path, words, dry_run, out)
        .with_context(|| format!("failed to process file: {}", path.display()))?;

    if matched_lines > 0 {
        stats.files_with_matches += 1;
        stats.total_matched_lines += matched_lines;
        stats.ops_per_file.insert(path.to_path_buf(), matched_lines);
    }

    Ok(())
}

fn is_yaml_file(path: &Path) -> bool {
    matches!(
        path.extension().and_then(OsStr::to_str).map(str::to_ascii_lowercase),
        Some(ext) if ext == "yaml" || ext == "yml"
    )
}

fn process_file(
    path: &Path,
    words: &[String],
    dry_run: bool,
    out: &mut dyn Write,
) -> Result<usize> {
    let file = fs::File::open(path)
        .with_context(|| format!("cannot open file for reading: {}", path.display()))?;

    let reader = BufReader::new(file);

    let mut output = Vec::new();
    let mut modified = false;
    let mut matched_lines = 0usize;

    for (idx, line_result) in reader.split(b'\n').enumerate() {
        let mut line = line_result
            .with_context(|| format!("failed reading line {} from {}", idx + 1, path.display()))?;

        let had_cr = line.ends_with(b"\r");
        if had_cr {
            line.pop();
        }

        let content = String::from_utf8_lossy(&line);

        if content.starts_with('#') {
            push_line(&mut output, &line, had_cr);
            continue;
        }

        let matched: Vec<&str> = words
            .iter()
            .map(String::as_str)
            .filter(|word| !word.is_empty() && content.contains(*word))
            .collect();

        if matched.is_empty() {
            push_line(&mut output, &line, had_cr);
            continue;
        }

        modified = true;
        matched_lines += 1;

        writeln!(
            out,
            "{}:{} -> {}",
            path.display(),
            idx + 1,
            matched.join(", ")
        )?;

        if dry_run {
            push_line(&mut output, &line, had_cr);
        } else {
            output.extend_from_slice(b"# ");
            output.extend_from_slice(&line);
            if had_cr {
                output.extend_from_slice(b"\r");
            }
            output.extend_from_slice(b"\n");
        }
    }

    // 注意：BufRead::split 会去掉分隔符，因此这里会统一在每行后补 '\n'。
    // 如果需要严格保持“最后一行无换行”的状态，见下方增强版本说明。
    if modified && !dry_run {
        write_file_atomic(path, &output)?;
    }

    Ok(matched_lines)
}

fn push_line(output: &mut Vec<u8>, line: &[u8], had_cr: bool) {
    output.extend_from_slice(line);

    if had_cr {
        output.extend_from_slice(b"\r");
    }

    output.extend_from_slice(b"\n");
}

fn write_file_atomic(path: &Path, data: &[u8]) -> Result<()> {
    let parent = path
        .parent()
        .with_context(|| format!("file has no parent directory: {}", path.display()))?;

    let metadata = fs::metadata(path).ok();
    let permissions = metadata.as_ref().map(|m| m.permissions());

    let mut tmp = NamedTempFile::new_in(parent)
        .with_context(|| format!("cannot create temporary file in {}", parent.display()))?;

    tmp.write_all(data)
        .with_context(|| format!("cannot write temporary file for {}", path.display()))?;

    tmp.as_file_mut()
        .sync_all()
        .with_context(|| format!("cannot sync temporary file for {}", path.display()))?;

    if let Some(permissions) = permissions {
        tmp.as_file()
            .set_permissions(permissions)
            .with_context(|| format!("cannot preserve permissions for {}", path.display()))?;
    }

    match tmp.persist(path) {
        Ok(_) => Ok(()),
        Err(err) => {
            #[cfg(windows)]
            {
                let tmp = err.file;

                // Windows 上目标文件存在时 rename/replace 行为更容易失败。
                // 这里退化为 remove + persist。注意：这一步严格来说不再是完全原子的。
                let _ = fs::remove_file(path);

                tmp.persist(path)
                    .map(|_| ())
                    .map_err(|err| err.error)
                    .with_context(|| format!("cannot replace file: {}", path.display()))
            }

            #[cfg(not(windows))]
            {
                Err(err.error).with_context(|| format!("cannot replace file: {}", path.display()))
            }
        }
    }
}

fn print_summary(folder_path: &Path, stats: &ProcStats) {
    println!();
    println!("Summary:");
    println!("  scanned folder: {}", folder_path.display());
    println!("  yaml files scanned: {}", stats.files_scanned);
    println!("  files with matches: {}", stats.files_with_matches);
    println!("  total matched lines: {}", stats.total_matched_lines);

    if !stats.ops_per_file.is_empty() {
        println!("  per-file operations:");
        for (file, count) in &stats.ops_per_file {
            println!("    {}: {}", file.display(), count);
        }
    }
}
