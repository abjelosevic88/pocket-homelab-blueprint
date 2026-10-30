import os

from fastapi import FastAPI

from .config import SECRET
from .routers import items

app = FastAPI()
app.include_router(items.router, prefix="/api")

DB_URL = os.environ.get("DATABASE_URL", "sqlite://")


@app.get("/health")
def health():
    """Liveness probe."""
    return {"ok": True, "secret": bool(SECRET)}


if __name__ == "__main__":
    print("run with uvicorn")
