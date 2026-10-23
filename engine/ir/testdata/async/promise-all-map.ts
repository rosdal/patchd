async function f(ids: string[]) {
  return Promise.all(ids.map(async (id) => (await fetch(id)).json()));
}
