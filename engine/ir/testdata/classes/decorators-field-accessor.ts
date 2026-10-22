declare const Input: () => PropertyDecorator;
declare const Log: MethodDecorator;
class Cmp {
  @Input() name = "";
  @Log
  get value() {
    return 1;
  }
}
