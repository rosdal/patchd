async function load(id: string) {
  const res = await fetch(`/api/${id}`);
  return res.json();
}
