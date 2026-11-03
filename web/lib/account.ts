import { useCallback, useEffect, useState } from "react";

// basePath is where the site lives, for what next doesn't prefix by itself, like fetch
export const basePath = "/poker";

export type Role = "root" | "admin" | "user";

export type Account = {
    id: string;
    username: string;
    role: Role;
    disabled: boolean;
    mustChange: boolean;
    createdAt: number;
    lastLogin: number;
};

export type Invite = {
    id: string;
    createdAt: number;
    expiresAt: number;
    revokedAt: number;
    usedBy: string;
    usedAt: number;
};

export function canCreateTables(a: Account) {
    return a.role === "root" || a.role === "admin";
}

// ApiError is a refused request, with the server's message to show as it is
export class ApiError extends Error {
    constructor(
        public status: number,
        message: string
    ) {
        super(message);
    }
}

// api calls the account API with a path under /poker/api
export async function api<T>(path: string, body?: unknown, post = body !== undefined): Promise<T> {
    let res: Response;
    try {
        res = await fetch(`${basePath}/api/${path}`, {
            method: post ? "POST" : "GET",
            headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
            body: body !== undefined ? JSON.stringify(body) : undefined,
        });
    } catch {
        throw new ApiError(0, "连不上服务器，请稍后再试");
    }
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
        throw new ApiError(res.status, data.error || "请求失败，请稍后再试");
    }
    return data as T;
}

function goToLogin() {
    const next = encodeURIComponent(window.location.pathname + window.location.search);
    window.location.href = `${basePath}/login?next=${next}`;
}

function goToChangePassword() {
    window.location.href = `${basePath}/login?change=1`;
}

// fetchAccount is who is logged in. It leaves for the login page if nobody is,
// or if they have to set a new password first, and then returns null.
async function fetchAccount(): Promise<Account | null> {
    try {
        const { account } = await api<{ account: Account }>("auth/me");
        if (account.mustChange) {
            goToChangePassword();
            return null;
        }
        return account;
    } catch (e) {
        if (e instanceof ApiError && e.status === 401) goToLogin();
        return null;
    }
}

// redirectIfLoggedOut is for when the websocket keeps being refused: only an
// ended login leaves the page, as a server restarting just needs more retries.
export function redirectIfLoggedOut() {
    fetchAccount();
}

export function useAccount() {
    const [account, setAccount] = useState<Account | null>(null);
    const refresh = useCallback(async () => setAccount(await fetchAccount()), []);
    useEffect(() => {
        refresh();
    }, [refresh]);
    return { account, setAccount, refresh };
}

export async function logout() {
    try {
        sessionStorage.removeItem("session");
    } catch {}
    await api("auth/logout", undefined, true).catch(() => {});
    window.location.href = `${basePath}/login`;
}
