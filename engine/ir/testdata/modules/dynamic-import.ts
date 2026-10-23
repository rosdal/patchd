export async function load(name: string) {
  const fixed = await import("./lib");
  const dyn = await import(name);
  return [fixed.version, dyn];
}
