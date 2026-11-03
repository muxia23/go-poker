import { FormEvent, ReactNode, useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { Button, PasswordInput, TextInput } from "@mantine/core";
import Confirm from "../components/Confirm";
import Layout from "../components/Layout";
import Section from "../components/Section";
import { Account, api, ApiError, Invite, logout, useAccount } from "../lib/account";

const message = (e: unknown) => (e instanceof ApiError ? e.message : "请求失败，请稍后再试");
const time = (ms: number) => (ms ? new Date(ms).toLocaleString("zh-CN", { hour12: false }) : "从未");
const roleNames = { root: "root", admin: "admin", user: "普通用户" };

function ErrorText({ text }: { text: string }) {
    return text ? (
        <p className="text-sm text-error" role="alert">
            {text}
        </p>
    ) : null;
}

// Secret shows something the server only gives out once, to pass on now
function Secret({ label, value, onClose }: { label: string; value: string; onClose: () => void }) {
    const [copied, setCopied] = useState(false);
    const copy = async () => {
        try {
            await navigator.clipboard.writeText(value);
            setCopied(true);
        } catch {} // no clipboard: it's on screen to select by hand
    };
    return (
        <div className="flex flex-col gap-2 rounded border border-line p-3" role="status">
            <p className="text-sm text-muted">{label}（只显示这一次，关闭后无法再次查看）</p>
            <code className="font-mono text-base break-all text-fg select-all">{value}</code>
            <div className="flex justify-end gap-2">
                <Button variant="subtle" size="xs" onClick={onClose}>
                    关闭
                </Button>
                <Button variant="filled" size="xs" onClick={copy}>
                    {copied ? "已复制" : "复制"}
                </Button>
            </div>
        </div>
    );
}

function RenameForm({ account, onDone }: { account: Account; onDone: (a: Account) => void }) {
    const [username, setUsername] = useState(account.username);
    const [error, setError] = useState("");
    const [done, setDone] = useState(false);
    const submit = async (e: FormEvent) => {
        e.preventDefault();
        setError("");
        setDone(false);
        try {
            const res = await api<{ account: Account }>("auth/username", { username });
            onDone(res.account);
            setDone(true);
        } catch (err) {
            setError(message(err));
        }
    };
    return (
        <form className="flex flex-col gap-2" onSubmit={submit}>
            <TextInput
                id="newUsername"
                label="用户名"
                description="在牌桌上时不能修改，请先离开牌桌"
                maxLength={20}
                value={username}
                onChange={(e) => setUsername(e.target.value)}
            />
            <ErrorText text={error} />
            {done && <p className="text-sm text-muted">用户名已修改</p>}
            <div>
                <Button type="submit" variant="filled" disabled={username === account.username || !username}>
                    保存用户名
                </Button>
            </div>
        </form>
    );
}

function PasswordForm() {
    const [current, setCurrent] = useState("");
    const [password, setPassword] = useState("");
    const [error, setError] = useState("");
    const [done, setDone] = useState(false);
    const submit = async (e: FormEvent) => {
        e.preventDefault();
        setError("");
        setDone(false);
        try {
            await api("auth/password", { current, password });
            setCurrent("");
            setPassword("");
            setDone(true);
        } catch (err) {
            setError(message(err));
        }
    };
    return (
        <form className="flex flex-col gap-2" onSubmit={submit}>
            <PasswordInput
                id="currentPassword"
                label="当前密码"
                autoComplete="current-password"
                value={current}
                onChange={(e) => setCurrent(e.target.value)}
            />
            <PasswordInput
                id="newPassword"
                label="新密码"
                description="至少 8 个字符。修改后其他设备会退出登录"
                autoComplete="new-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
            />
            <ErrorText text={error} />
            {done && <p className="text-sm text-muted">密码已修改</p>}
            <div>
                <Button type="submit" variant="filled" disabled={!current || !password}>
                    修改密码
                </Button>
            </div>
        </form>
    );
}

function AccountRow({
    account,
    onRole,
    onDisabled,
    onReset,
}: {
    account: Account;
    onRole: (a: Account) => void;
    onDisabled: (a: Account) => void;
    onReset: (a: Account) => void;
}) {
    const root = account.role === "root";
    return (
        <li className="flex flex-col gap-2 border-t border-line pt-3 first:border-t-0 first:pt-0">
            <div className="flex flex-wrap items-baseline gap-x-2">
                <span className="min-w-0 font-semibold break-all text-fg">{account.username}</span>
                <span className="text-sm text-muted">{roleNames[account.role]}</span>
                {account.disabled && <span className="text-sm text-error">已禁用</span>}
            </div>
            <p className="text-xs text-subtle">
                注册 {time(account.createdAt)} · 最近登录 {time(account.lastLogin)}
            </p>
            {!root && (
                <div className="flex flex-wrap gap-2">
                    <Button size="compact-xs" onClick={() => onRole(account)}>
                        {account.role === "admin" ? "降为普通用户" : "设为 admin"}
                    </Button>
                    <Button size="compact-xs" onClick={() => onReset(account)}>
                        重置密码
                    </Button>
                    <Button
                        size="compact-xs"
                        variant="outline"
                        color={account.disabled ? undefined : "red"}
                        onClick={() => onDisabled(account)}
                    >
                        {account.disabled ? "启用" : "禁用"}
                    </Button>
                </div>
            )}
        </li>
    );
}

type Pending = { title: string; text: string; action: string; run: () => Promise<void> };

function ManageAccounts() {
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [error, setError] = useState("");
    const [temp, setTemp] = useState<{ name: string; password: string } | null>(null);
    const [pending, setPending] = useState<Pending | null>(null);

    const load = useCallback(async () => {
        try {
            setAccounts((await api<{ accounts: Account[] }>("admin/accounts")).accounts);
        } catch (e) {
            setError(message(e));
        }
    }, []);
    useEffect(() => {
        load();
    }, [load]);

    const run = async (f: () => Promise<void>) => {
        setError("");
        try {
            await f();
        } catch (e) {
            setError(message(e));
        }
        load();
    };
    const path = (a: Account, action: string) => `admin/accounts/${encodeURIComponent(a.id)}/${action}`;
    const setRole = (a: Account) =>
        run(() => api(path(a, "role"), { role: a.role === "admin" ? "user" : "admin" }));
    const setDisabled = (a: Account, disabled: boolean) => api<unknown>(path(a, "disabled"), { disabled });
    const reset = async (a: Account) => {
        const { password } = await api<{ password: string }>(path(a, "reset-password"), undefined, true);
        setTemp({ name: a.username, password });
    };

    return (
        <Section title="账号管理">
            <ErrorText text={error} />
            {temp && (
                <Secret label={`${temp.name} 的临时密码`} value={temp.password} onClose={() => setTemp(null)} />
            )}
            <ul className="flex flex-col gap-3">
                {accounts.map((a) => (
                    <AccountRow
                        key={a.id}
                        account={a}
                        onRole={setRole}
                        onDisabled={(a) =>
                            a.disabled
                                ? run(async () => {
                                      await setDisabled(a, false);
                                  })
                                : setPending({
                                      title: `禁用 ${a.username}？`,
                                      text: "对方会立刻掉线，并且无法再登录。之后可以重新启用。",
                                      action: "禁用",
                                      run: async () => {
                                          await setDisabled(a, true);
                                      },
                                  })
                        }
                        onReset={(a) =>
                            setPending({
                                title: `重置 ${a.username} 的密码？`,
                                text: "对方会立刻掉线，下次要用临时密码登录并设置新密码。",
                                action: "重置",
                                run: () => reset(a),
                            })
                        }
                    />
                ))}
            </ul>
            {pending && (
                <Confirm
                    title={pending.title}
                    message={pending.text}
                    action={pending.action}
                    onCancel={() => setPending(null)}
                    onConfirm={() => {
                        const p = pending;
                        setPending(null);
                        run(p.run);
                    }}
                />
            )}
        </Section>
    );
}

function inviteStatus(i: Invite): { text: string; open: boolean } {
    if (i.usedAt) return { text: `已使用 · ${i.usedBy || "账号不存在"}`, open: false };
    if (i.revokedAt) return { text: "已作废", open: false };
    if (i.expiresAt <= Date.now()) return { text: "已过期", open: false };
    return { text: "未使用", open: true };
}

function ManageInvites() {
    const [invites, setInvites] = useState<Invite[]>([]);
    const [code, setCode] = useState("");
    const [error, setError] = useState("");

    const load = useCallback(async () => {
        try {
            setInvites((await api<{ invites: Invite[] }>("admin/invites")).invites);
        } catch (e) {
            setError(message(e));
        }
    }, []);
    useEffect(() => {
        load();
    }, [load]);

    const create = async () => {
        setError("");
        try {
            setCode((await api<{ code: string }>("admin/invites", undefined, true)).code);
        } catch (e) {
            setError(message(e));
        }
        load();
    };
    const revoke = async (id: string) => {
        setError("");
        try {
            await api(`admin/invites/${encodeURIComponent(id)}/revoke`, undefined, true);
        } catch (e) {
            setError(message(e));
        }
        load();
    };

    return (
        <Section title="邀请码">
            <div>
                <Button variant="filled" onClick={create}>
                    生成邀请码
                </Button>
            </div>
            <p className="text-xs text-subtle">每个码只能注册一个账号，7 天内有效</p>
            <ErrorText text={error} />
            {code && <Secret label="新邀请码" value={code} onClose={() => setCode("")} />}
            <ul className="flex flex-col gap-2">
                {invites.map((i) => {
                    const status = inviteStatus(i);
                    return (
                        <li
                            key={i.id}
                            className="flex items-center justify-between gap-2 border-t border-line pt-2 first:border-t-0 first:pt-0"
                        >
                            <div className="min-w-0">
                                <p className="text-sm break-all text-fg">{status.text}</p>
                                <p className="text-xs text-subtle">
                                    生成 {time(i.createdAt)} · 到期 {time(i.expiresAt)}
                                </p>
                            </div>
                            {status.open && (
                                <Button
                                    size="compact-xs"
                                    variant="outline"
                                    color="red"
                                    className="shrink-0"
                                    onClick={() => revoke(i.id)}
                                >
                                    作废
                                </Button>
                            )}
                        </li>
                    );
                })}
            </ul>
        </Section>
    );
}

function Page({ children }: { children: ReactNode }) {
    return (
        <Layout title="账号">
            <div className="mx-auto flex w-full max-w-xl flex-col gap-5 px-4 py-6">{children}</div>
        </Layout>
    );
}

// AccountsPage is everyone's own account, and for root everyone else's and the invite codes.
// The server decides who gets what; this only leaves out what would be refused.
export default function AccountsPage() {
    const { account, setAccount } = useAccount();
    if (!account) return <Page>{null}</Page>;
    return (
        <Page>
            <header className="flex items-center justify-between gap-2">
                <h1 className="min-w-0 text-xl font-semibold break-all text-fg">{account.username}</h1>
                <div className="flex shrink-0 gap-2">
                    <Button component={Link} href="/" variant="subtle" size="compact-sm">
                        返回牌局
                    </Button>
                    <Button variant="subtle" size="compact-sm" onClick={logout}>
                        退出
                    </Button>
                </div>
            </header>
            <Section title="我的账号">
                <RenameForm account={account} onDone={setAccount} />
                <PasswordForm />
            </Section>
            {account.role === "root" && (
                <>
                    <ManageInvites />
                    <ManageAccounts />
                </>
            )}
        </Page>
    );
}
