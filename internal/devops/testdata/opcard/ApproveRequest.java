package com.example.orders.api;

@Data
public class ApproveRequest {
    private static final long serialVersionUID = 1L;
    public static final String DEFAULT_CHANNEL = "MARKER_CONST";

    @NotNull
    @JsonProperty("amount")
    private BigDecimal amount;
    private ChannelInfo channel;
    private List<String> tags = new ArrayList<>();
    private java.time.LocalDate valueDate;

    public BigDecimal getAmount() {
        String x = "MARKER_BODY";
        return amount;
    }

    public static class Inner {
        private String notMine;
    }
}
