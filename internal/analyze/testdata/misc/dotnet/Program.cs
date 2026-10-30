var app = WebApplication.Create(args);
var todos = app.MapGroup("/todos");
todos.MapGet("/{id}", (int id) => Results.Ok(id));
app.MapPost("/login", Login);
app.Run();
