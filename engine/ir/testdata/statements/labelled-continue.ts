function f(rows: number[][]) {
  let n = 0;
  rows: for (const row of rows) {
    for (const x of row) {
      if (x === 0) continue rows;
      n++;
    }
  }
  return n;
}
