'use client';
export const dynamic = 'force-dynamic';
import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { api, ApiError } from '@/lib/api';
export default function ResetPasswordPage(){
 const router=useRouter(); const [token,setToken]=useState(''); useEffect(()=>{setToken(new URLSearchParams(window.location.search).get('token')||'');},[]);
 const [password,setPassword]=useState(''),[confirm,setConfirm]=useState(''),[busy,setBusy]=useState(false),[done,setDone]=useState(false),[error,setError]=useState<string|null>(null);
 async function submit(e:React.FormEvent){e.preventDefault();setError(null);if(password!==confirm){setError('As passwords não coincidem.');return;}setBusy(true);try{await api.postPublic('/api/v1/auth/reset-password',{token,password});setDone(true)}catch(err){setError(err instanceof ApiError?err.message:'Não foi possível redefinir a password.')}finally{setBusy(false)}}
 return <div className="flex min-h-screen items-center justify-center bg-[radial-gradient(ellipse_at_top,rgb(var(--color-primary)/0.12),transparent_60%)] px-4"><div className="w-full max-w-sm"><div className="card p-6"><h1 className="text-xl font-semibold">Nova password</h1>{done?<><p className="mt-2 text-sm text-nd-muted">A password foi alterada. As sessões anteriores foram terminadas.</p><button className="btn-primary mt-6 w-full" onClick={()=>router.replace('/login')}>Voltar ao login</button></>:<form onSubmit={submit} className="mt-5 space-y-4"><div><label className="label">Nova password</label><input className="input" type="password" minLength={12} value={password} onChange={e=>setPassword(e.target.value)} required /></div><div><label className="label">Confirmar password</label><input className="input" type="password" minLength={12} value={confirm} onChange={e=>setConfirm(e.target.value)} required /></div>{error&&<div className="rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger">{error}</div>}<button className="btn-primary w-full" disabled={busy}>{busy?'A guardar…':'Definir nova password'}</button></form>}</div></div></div>
}
