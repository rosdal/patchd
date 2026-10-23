function f(lock: { acquire(): void; release(): void }) {
  lock.acquire();
  try {
    return work();
  } finally {
    lock.release();
  }
}
declare function work(): number;
