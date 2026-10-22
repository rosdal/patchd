function* echo() {
  let x = yield;
  while (true) x = yield x;
}
