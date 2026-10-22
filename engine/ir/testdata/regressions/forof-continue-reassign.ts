// From vscode's InMemoryFileSystemProvider._lookup (MIT): a variable
// reassigned in a for…of body whose iteration can continue early.
function lookup(root: any, path: string, silent: boolean) {
  const parts = path.split("/");
  let entry = root;
  for (const part of parts) {
    if (!part) {
      continue;
    }
    let child;
    if (entry.entries) {
      child = entry.entries.get(part);
    }
    if (!child) {
      if (!silent) {
        throw new Error("not found");
      } else {
        return undefined;
      }
    }
    entry = child;
  }
  return entry;
}
