// Conditional and mapped types
type IsString<T> = T extends string ? "yes" : "no";
const yes: IsString<"x"> = "yes";
//    ^? "yes"
type Optional<T> = { [K in keyof T]?: T[K] };
interface User {
  id: number;
  name: string;
}
const partial: Optional<User> = {};
//    ^? Optional<User>
const id = partial.id;
//    ^? number | undefined
type Getters<T> = { [K in keyof T as `get${Capitalize<string & K>}`]: () => T[K] };
declare const getters: Getters<User>;
const getName = getters.getName;
//    ^? () => string
export { yes, id, getName };
