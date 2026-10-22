declare function Inject(t: string): ParameterDecorator;
class Svc {
  constructor(@Inject("DB") private db: any) {}
}
