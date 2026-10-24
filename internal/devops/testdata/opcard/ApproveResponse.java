package com.example.orders.api;

public class ApproveResponse {
    private ApprovalStatus status;
    private String message = "MARKER_DEFAULT";

    public ApproveResponse(ApprovalStatus status) { this.status = status; }
}
