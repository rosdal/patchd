async function f(tasks: (() => Promise<void>)[]) {
  for (const t of tasks) {
    await t();
  }
}
