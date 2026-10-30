export async function GET(req: Request) {
  return Response.json({});
}

export async function DELETE() {
  return new Response(null, { status: 204 });
}
