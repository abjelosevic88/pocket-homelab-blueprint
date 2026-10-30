from flask import Flask, Blueprint

app = Flask(__name__)
bp = Blueprint("admin", __name__, url_prefix="/admin")


@app.route("/login", methods=["GET", "POST"])
def login():
    return ""


@bp.get("/users/<int:user_id>")
def user(user_id):
    return ""


app.register_blueprint(bp)
