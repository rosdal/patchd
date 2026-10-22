function find(grid: number[][], v: number) {
  let at = -1;
  outer: for (let i = 0; i < grid.length; i++) {
    for (let j = 0; j < grid[i].length; j++) {
      if (grid[i][j] === v) {
        at = i;
        break outer;
      }
    }
  }
  return at;
}
