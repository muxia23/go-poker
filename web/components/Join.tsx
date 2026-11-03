import React, { useCallback, useContext, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/router";
import { useSocket } from "../hooks/useSocket";
import { AppContext } from "../providers/AppStore";
import { FiGithub } from "react-icons/fi";
import ColorSchemeToggle from "./ColorSchemeToggle";
import { joinTable } from "../actions/actions";
import { ActionIcon, Button, PasswordInput, TextInput } from "@mantine/core";
import { invitedPassword, invitedTable } from "../lib/invite";
import { canCreateTables, logout, useAccount } from "../lib/account";
import CreateTable from "./CreateTable";
import Features from "./Features";

function randomTableGen(length: number): string {
    let chars = "abcdefghijklmnopqrstuvwxyz0123456789";
    let result: string = "";
    for (let i = 0; i < length; i++) {
        result += chars[Math.floor(Math.random() * chars.length)];
    }
    return result;
}

export default function Join() {
    const socket = useSocket();
    const { appState, dispatch } = useContext(AppContext);
    const { account } = useAccount();
    const username = account?.username ?? "";
    const [tablename, setTablename] = useState("");
    const [password, setPassword] = useState("");
    const [invitedWithPassword, setInvitedWithPassword] = useState(false);
    const [creating, setCreating] = useState(false);
    const connected = appState.connection === "open";
    let invalid = username == "" || tablename == "" || !connected;
    const showPassword = appState.needPassword || invitedWithPassword;

    // an invite link fills in the table and any password, so only joining is left. Wait for
    // the router, whose first replace of the URL would put the password back.
    const router = useRouter();
    useEffect(() => {
        if (!router.isReady) return;
        setTablename(invitedTable());
        // the first call takes the password out of the address bar, so ignore later empty ones
        const invited = invitedPassword();
        if (invited) {
            setPassword(invited);
            setInvitedWithPassword(true);
        }
    }, [router.isReady]);

    // the table shows once the server confirms with joined-table
    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault();
        if (socket && !invalid) {
            joinTable(socket, tablename, username, undefined, password || undefined);
        }
    };

    // creating asks for the table's settings first
    const handleCreate = () => {
        if (invalid) return;
        dispatch({ type: "setError", payload: null });
        setCreating(true);
    };

    const handleTablename = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
        setTablename(e.target.value);
    }, []);

    return (
        <div className="relative flex min-h-screen flex-col">
            <div className="absolute top-3 right-4 flex items-center gap-2">
                <ColorSchemeToggle size="md" />
                <ActionIcon component="a" size="md" href="https://github.com/evanofslack/go-poker" title="source on github" aria-label="source on github">
                    <FiGithub />
                </ActionIcon>
            </div>
            <div className="flex flex-col items-center pt-[14vh]">
                <h1 className="mb-10 text-4xl font-semibold text-fg sm:mb-16 sm:text-5xl">
                    GoPoker
                </h1>
                {account && (
                    <p className="mb-4 flex items-center gap-3 text-sm text-muted">
                        <span className="font-semibold text-fg">{account.username}</span>
                        <Button component={Link} href="/accounts" variant="subtle" size="compact-xs">
                            账号
                        </Button>
                        <Button variant="subtle" size="compact-xs" onClick={logout}>
                            退出
                        </Button>
                    </p>
                )}
                <form
                    className="flex w-full max-w-xs flex-col gap-2 px-4 sm:w-auto sm:max-w-none sm:flex-row sm:gap-0 sm:px-0"
                    onSubmit={handleSubmit}
                >
                    <TextInput
                        autoFocus
                        size="md"
                        className="sm:my-2 sm:mr-2 sm:w-48"
                        id="tablename"
                        maxLength={32}
                        value={tablename}
                        placeholder="table"
                        onChange={handleTablename}
                        rightSectionWidth="auto"
                        rightSection={
                            <Button
                                variant="subtle"
                                size="xs"
                                className="mr-1.5"
                                onClick={() => setTablename(randomTableGen(5))}
                            >
                                random
                            </Button>
                        }
                    />
                    {showPassword && (
                        <PasswordInput
                            autoFocus={appState.needPassword}
                            size="md"
                            className="sm:my-2 sm:mr-2 sm:w-40"
                            id="password"
                            maxLength={32}
                            value={password}
                            placeholder="password"
                            aria-label="table password"
                            onChange={(e) => setPassword(e.target.value)}
                        />
                    )}
                    {account && canCreateTables(account) && (
                        <Button
                            type="button"
                            size="md"
                            className="sm:my-2 sm:mr-2"
                            disabled={invalid}
                            onClick={handleCreate}
                        >
                            create
                        </Button>
                    )}
                    <Button type="submit" variant="filled" size="md" className="sm:my-2" disabled={invalid}>
                        join
                    </Button>
                </form>
                {appState.error && (
                    <p className="mt-2 px-4 text-center text-error">{appState.error}</p>
                )}
                {!connected && <p className="mt-2 text-subtle">connecting to server…</p>}
                {creating && (
                    <CreateTable
                        tablename={tablename}
                        username={username}
                        onClose={() => setCreating(false)}
                    />
                )}
            </div>
            <Features />
        </div>
    );
}
