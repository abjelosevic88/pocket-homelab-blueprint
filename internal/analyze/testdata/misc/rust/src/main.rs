use axum::{routing::get, Router};
use serde::Serialize;

/// A user record.
#[derive(Serialize)]
pub struct User {
    id: u64,
}

pub trait Store {
    fn get(&self, id: u64) -> Option<User>;
}

impl User {
    pub fn new(id: u64) -> Self {
        User { id }
    }
}

async fn list_users() -> &'static str {
    "[]"
}

async fn create_user() -> &'static str {
    "{}"
}

#[tokio::main]
async fn main() {
    let port = std::env::var("PORT").unwrap_or("3000".to_string());
    let app = Router::new()
        .route("/users", get(list_users).post(create_user))
        .route("/users/:id", get(list_users));
    let _ = (app, port);
}
