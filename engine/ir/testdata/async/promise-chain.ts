function f(url: string) {
  return fetch(url)
    .then((r) => r.json())
    .catch((e) => console.error(e))
    .finally(() => console.log("done"));
}
