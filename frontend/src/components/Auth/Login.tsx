import { useState, type FormEvent } from "react";
import { useAuth } from "../../context/AuthContext";

export default function Login() {
  const { login, register } = useAuth();
  const [mode, setMode] = useState<"login" | "register">("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");

    if (mode === "register") {
      if (password.length < 6) {
        setError("Password must be at least 6 characters");
        return;
      }
      if (password !== confirm) {
        setError("Passwords do not match");
        return;
      }
    }

    setLoading(true);
    try {
      if (mode === "login") {
        await login(email, password);
      } else {
        await register(email, password);
      }
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setLoading(false);
    }
  };

  const switchMode = () => {
    setMode(mode === "login" ? "register" : "login");
    setError("");
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-slate-950 px-4">
      <div className="w-full max-w-sm">
        <div className="flex items-center justify-center gap-3 mb-8">
          <div className="w-10 h-10 bg-linear-to-br from-cyan-500 to-violet-500 rounded-lg flex items-center justify-center text-white font-extrabold text-lg">
            F
          </div>
          <span className="text-2xl font-bold bg-linear-to-br from-cyan-500 to-violet-500 bg-clip-text text-transparent">
            FinTrak
          </span>
        </div>

        <div className="bg-slate-900 border border-slate-800 rounded-2xl p-6 shadow-xl">
          <h1 className="text-xl font-bold text-slate-100 mb-1">
            {mode === "login" ? "Welcome back" : "Create your account"}
          </h1>
          <p className="text-sm text-slate-500 mb-6">
            {mode === "login"
              ? "Sign in to continue to FinTrak"
              : "Start tracking your finances in minutes"}
          </p>

          {error && (
            <div className="mb-4 px-3 py-2 rounded-lg bg-red-500/10 border border-red-500/30 text-sm text-red-400">
              {error}
            </div>
          )}

          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label className="block text-xs font-medium text-slate-400 mb-1">
                Email
              </label>
              <input
                type="email"
                required
                autoFocus
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@example.com"
                className="w-full px-3 py-2.5 bg-slate-950 border border-slate-700 rounded-lg text-sm text-white placeholder:text-slate-600 focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-400 mb-1">
                Password
              </label>
              <input
                type="password"
                required
                minLength={mode === "register" ? 6 : undefined}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                className="w-full px-3 py-2.5 bg-slate-950 border border-slate-700 rounded-lg text-sm text-white placeholder:text-slate-600 focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
              />
            </div>
            {mode === "register" && (
              <div>
                <label className="block text-xs font-medium text-slate-400 mb-1">
                  Confirm Password
                </label>
                <input
                  type="password"
                  required
                  minLength={6}
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  placeholder="••••••••"
                  className="w-full px-3 py-2.5 bg-slate-950 border border-slate-700 rounded-lg text-sm text-white placeholder:text-slate-600 focus:outline-none focus:border-cyan-500 focus:ring-1 focus:ring-cyan-500 transition-all"
                />
              </div>
            )}
            <button
              type="submit"
              disabled={loading}
              className="w-full py-2.5 bg-cyan-500 hover:bg-cyan-400 disabled:opacity-50 disabled:cursor-not-allowed text-white text-sm font-semibold rounded-lg transition-all shadow-lg shadow-cyan-500/20"
            >
              {loading
                ? mode === "login"
                  ? "Signing in..."
                  : "Creating account..."
                : mode === "login"
                  ? "Sign In"
                  : "Create Account"}
            </button>
          </form>

          <div className="mt-5 pt-5 border-t border-slate-800 text-center text-sm">
            <span className="text-slate-500">
              {mode === "login"
                ? "Don't have an account?"
                : "Already have an account?"}
            </span>{" "}
            <button
              type="button"
              onClick={switchMode}
              className="text-cyan-500 hover:text-cyan-400 font-medium"
            >
              {mode === "login" ? "Create one" : "Sign in"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
