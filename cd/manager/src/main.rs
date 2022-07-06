#[macro_use]
extern crate rocket;

#[get("/health")]
fn index() -> &'static str {
    "OK"
}

#[launch]
fn rocket() -> _ {
    rocket::build().mount("/", routes![index])
}
