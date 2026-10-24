package com.example.orders.api;

public record ChannelInfo(@JsonProperty("c") String code, String name, ChannelInfo parent) {
}
