import { useEffect, useState } from 'react';
import { BookOpenCheck } from 'lucide-react';
import { api, apiExt, type Survey, type Tally } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, statusTone } from '../components/ui';

interface Question {
  id: number;
  text: string;
  multi: boolean;
}

interface Option {
  id: number;
  label: string;
}

export function Surveys() {
  const { token, login } = useAuth();
  const { t } = useLang();
  const [surveys, setSurveys] = useState<Survey[]>([]);
  const [error, setError] = useState('');
  const [title, setTitle] = useState('');
  const [selSurvey, setSelSurvey] = useState<number | null>(null);
  const [questions, setQuestions] = useState<Question[]>([]);
  const [qtext, setQtext] = useState('');
  const [selQuestion, setSelQuestion] = useState<number | null>(null);
  const [options, setOptions] = useState<Option[]>([]);
  const [olabel, setOlabel] = useState('');
  const [tally, setTally] = useState<Tally[]>([]);

  const surveyName = (s: number) =>
    s === 0 ? t('stDraft') : s === 1 ? t('stOpen') : t('stClosed');

  const reload = () => {
    if (token) api.surveys(token).then(setSurveys).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const act = (fn: Promise<unknown>, after?: () => void) =>
    fn.then(() => {
      setError('');
      reload();
      after?.();
    }).catch((e: Error) => setError(e.message));

  const openSurvey = (sid: number) => {
    if (!token) return;
    setSelSurvey(sid);
    setSelQuestion(null);
    apiExt.surveyQuestions(token, sid).then(setQuestions).catch((e: Error) => setError(e.message));
  };

  const openQuestion = (qid: number) => {
    if (!token) return;
    setSelQuestion(qid);
    apiExt.questionOptions(token, qid).then(setOptions).catch((e: Error) => setError(e.message));
    api.questionResults(token, qid).then(setTally).catch(() => undefined);
  };

  const vote = (qid: number, optId: number) => {
    if (!token) return;
    act(
      apiExt.castVote(token, qid, { user_login: login, option_ids: [optId] }),
      () => openQuestion(qid),
    );
  };

  return (
    <div>
      <PageHeader icon={<BookOpenCheck size={22} />} title={t('surveys')} />
      {error && <Alert>{error}</Alert>}
      <div className="grid two">
        <Card title={t('surveys')}>
          <ul className="clean">
            {surveys.map((s) => (
              <li key={s.id}>
                <button onClick={() => openSurvey(s.id)} title={t('details')}>
                  {s.title}
                </button>
                <Badge tone={statusTone(s.status)}>{surveyName(s.status)}</Badge>
                {s.status === 0 && token && (
                  <button onClick={() => act(api.setSurveyStatus(token, s.id, 1, s.row_version))}>
                    {t('open')}
                  </button>
                )}
                {s.status === 1 && token && (
                  <button onClick={() => act(api.setSurveyStatus(token, s.id, 2, s.row_version))}>
                    {t('close')}
                  </button>
                )}
              </li>
            ))}
          </ul>
          {surveys.length === 0 && <p className="muted">{t('noData')}</p>}
          <h4>{t('newSurvey')}</h4>
          <label className="field">
            {t('title')} <input value={title} onChange={(e) => setTitle(e.target.value)} />
          </label>
          <button className="primary" onClick={() => token && act(api.createSurvey(token, { title }))}>
            {t('create')}
          </button>
        </Card>
        <Card title={selSurvey ? `${t('question')} — ${t('surveys')}` : t('question')}>
          {!selSurvey && <p className="muted">{t('noData')}</p>}
          {selSurvey !== null && (
            <>
              <ul className="clean">
                {questions.map((q) => (
                  <li key={q.id}>
                    <button onClick={() => openQuestion(q.id)}>{q.text}</button>
                    {q.multi && <Badge>multi</Badge>}
                  </li>
                ))}
              </ul>
              <label className="field">
                {t('newQuestion')} <input value={qtext} onChange={(e) => setQtext(e.target.value)} />
              </label>
              <button
                className="primary"
                onClick={() =>
                  token &&
                  selSurvey !== null &&
                  act(apiExt.createQuestion(token, selSurvey, { text: qtext, multi: false }), () =>
                    openSurvey(selSurvey),
                  )
                }
              >
                {t('add')}
              </button>
            </>
          )}
        </Card>
      </div>
      {selQuestion !== null && (
        <Card title={`${t('votes')} & ${t('details')}`}>
          <ul className="clean">
            {options.map((o) => (
              <li key={o.id}>
                <span>{o.label}</span>
                {token && <button onClick={() => vote(selQuestion, o.id)}>{t('votes')}</button>}
              </li>
            ))}
          </ul>
          <h4>{t('votes')}</h4>
          <ul className="clean">
            {tally.map((x) => (
              <li key={x.option_id}>
                <span>
                  {x.label}: <strong>{x.votes}</strong>
                </span>
              </li>
            ))}
          </ul>
          <label className="field">
            {t('newOption')} <input value={olabel} onChange={(e) => setOlabel(e.target.value)} />
          </label>
          <button
            onClick={() =>
              token &&
              selQuestion !== null &&
              act(apiExt.addOption(token, selQuestion, { label: olabel }), () =>
                openQuestion(selQuestion),
              )
            }
          >
            {t('add')}
          </button>
        </Card>
      )}
    </div>
  );
}
