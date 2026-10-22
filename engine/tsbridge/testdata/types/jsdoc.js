// JSDoc types in JavaScript
/**
 * @param {number} a
 * @param {number} b
 * @returns {number}
 */
export function sum(a, b) {
  return a + b;
}
const total = sum(1, 2);
//    ^? number
/** @type {Array<string>} */
const names = [];
//    ^? string[]
export { total, names };
