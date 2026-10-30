package com.acme;

import org.springframework.web.bind.annotation.*;

/** Users over HTTP. */
@RestController
@RequestMapping("/api/users")
public class UserController {

    @GetMapping("/{id}")
    public User get(@PathVariable long id) {
        return new User(System.getenv("DEFAULT_NAME"));
    }

    @PostMapping
    public User create(@RequestBody User u) {
        return u;
    }

    @RequestMapping(value = "/search", method = RequestMethod.GET)
    public String search() {
        return "";
    }
}
