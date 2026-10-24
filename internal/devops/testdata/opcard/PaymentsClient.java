package com.example.orders.client;

@FeignClient(name = "payments-api", url = "${payments.url}")
@RequestMapping("/payments")
public interface PaymentsClient {

    @PostMapping("/authorize")
    AuthResponse authorize(@RequestBody AuthRequest req);

    @GetMapping("/{id}")
    AuthResponse get(@PathVariable("id") String id);
}
