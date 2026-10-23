class Svc {
  name = "svc";
  run(items: string[]) {
    let count = 0;
    items.forEach((i) => {
      count++;
      console.log(this.name, i, count);
    });
    return count;
  }
}
