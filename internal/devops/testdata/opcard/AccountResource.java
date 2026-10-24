package com.example.accounts;

@Path("/accounts")
public class AccountResource {

    @GET
    @Path("/{accountId}/balance")
    public BalanceView balance(@PathParam("accountId") String accountId) {
        return accountManager.balance(accountId);
    }

    @POST
    public AccountView open(OpenAccountRequest req) {
        return accountManager.open(req);
    }
}
