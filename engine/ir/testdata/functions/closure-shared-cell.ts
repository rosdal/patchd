function pair() {
  let v = 0;
  const get = () => v;
  const set = (x: number) => {
    v = x;
  };
  return { get, set };
}
