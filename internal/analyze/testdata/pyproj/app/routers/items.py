from fastapi import APIRouter

from ..models import Item

router = APIRouter(prefix="/items", tags=["items"])


@router.get("/{item_id}")
async def read_item(item_id: int):
    return Item(item_id)


@router.post("")
def create_item(
    name: str,
):
    return Item(0)
