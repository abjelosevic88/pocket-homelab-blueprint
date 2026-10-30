MAX_ITEMS = 100


class Item:
    """An item in the shop."""

    def __init__(self, id):
        self.id = id

    def total(self):
        return 0


def _private():
    pass
