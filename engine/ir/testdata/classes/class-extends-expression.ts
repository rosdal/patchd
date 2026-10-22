declare function mixin<T>(b: T): T;
class Base {}
class M extends mixin(Base) {}
