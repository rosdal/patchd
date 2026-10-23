declare function useState<T>(v: T): [T, (v: T) => void];
export function C() {
  const [count, setCount] = useState(0);
  setCount(count + 1);
  return count;
}
