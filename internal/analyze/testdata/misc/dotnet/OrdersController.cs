namespace Shop.Api;

[ApiController]
[Route("api/[controller]")]
public class OrdersController : ControllerBase
{
    [HttpGet("{id:int}")]
    public IActionResult Get(int id)
    {
        var key = Environment.GetEnvironmentVariable("ORDERS_KEY");
        return Ok();
    }

    [HttpPost]
    public IActionResult Create()
    {
        return Ok();
    }
}
