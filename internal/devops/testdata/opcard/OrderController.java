package com.example.orders.web;

import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping(value = "/orders", produces = "application/json")
public class OrderController {

    @Autowired
    private OrderService orderService;

    @GetMapping("/{id}")
    public OrderView get(@PathVariable("id") long id) {
        return orderService.get(id);
    }

    // Aynı rota, farklı yöntem: approve DEĞİL.
    @GetMapping("/{id}/approve")
    public ApprovalView approvalStatus(@PathVariable long id) {
        return orderService.status(id);
    }

    /**
     * Onay — yorumdaki orderService.notThis(x) çağrı sayılmaz.
     */
    @PostMapping(path = "/{id}/approve", consumes = "application/json")
    @Timed("orders.approve")
    public ResponseEntity<ApproveResponse> approve(@PathVariable("id") long id,
                                                   @Valid @RequestBody ApproveRequest req) throws OrderException {
        String marker = "MARKER_42 } kapanış dizede";
        ApproveResponse r = orderService.approve(id, req);
        auditClient.record("/audit/events", id);
        return ResponseEntity.ok(r);
    }

    @Autowired
    private AuditClient auditClient;
}
