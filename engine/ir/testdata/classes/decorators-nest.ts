declare function Controller(p: string): ClassDecorator;
declare function Post(): MethodDecorator;
declare function Body(): ParameterDecorator;
declare function Param(n: string): ParameterDecorator;
@Controller("users")
export class UsersController {
  @Post()
  create(@Body() dto: any, @Param("id") id: string) {
    return dto;
  }
}
