package com.example.orders.service;

@Service
public class OrderServiceImpl implements OrderService {

    private final PaymentsClient paymentsClient;
    private final OrderRepository orderRepository;
    private final KafkaTemplate<String, String> kafkaTemplate;
    private final JdbcTemplate jdbcTemplate;

    @Override
    @Transactional
    public ApproveResponse approve(long id, ApproveRequest req) {
        Order o = orderRepository.findById(id).orElseThrow();
        AuthResponse auth = paymentsClient.authorize(new AuthRequest(o.getId(), req.getAmount()));
        jdbcTemplate.update("UPDATE orders SET status = 'MARKER_SQL' WHERE id = ?", id);
        kafkaTemplate.send("order-events", String.valueOf(id), "MARKER_PAYLOAD");
        return new ApproveResponse(auth.getStatus());
    }
}
