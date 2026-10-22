class Timer {
  ticks = 0;
  start() {
    setInterval(() => {
      this.ticks++;
    }, 1000);
  }
}
