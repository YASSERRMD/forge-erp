import { useEffect, useState } from 'react';
import { BookOpenCheck } from 'lucide-react';
import { api, apiExt, type Survey, type Tally } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Alert, Badge, Card, PageHeader, statusTone } from '../components/ui';

const surveyStatus: Record<number, string> = {
  0: 'Draft',
  1: 'Open',
  2: 'Closed',
};

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
      <PageHeader icon={<BookOpenCheck size={22} />} title="Surveys" sub="Polls, ballots and tallies" />
      {error && <Alert>{error}</Alert>}
      <div className="grid two">
        <Card title="Surveys">
          <ul className="clean">
            {surveys.map((s) => (
              <li key={s.id}>
                <button onClick={() => openSurvey(s.id)} title="Open questions">
                  {s.title}
                </button>
                <Badge tone={statusTone(s.status)}>{surveyStatus[s.status] ?? s.status}</Badge>
                {s.status === 0 && token && (
                  <button onClick={() => act(api.setSurveyStatus(token, s.id, 1, s.row_version))}>
                    Open
                  </button>
                )}
                {s.status === 1 && token && (
                  <button onClick={() => act(api.setSurveyStatus(token, s.id, 2, s.row_version))}>
                    Close
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h4>New survey</h4>
          <label className="field">
            Title <input value={title} onChange={(e) => setTitle(e.target.value)} />
          </label>
          <button className="primary" onClick={() => token && act(api.createSurvey(token, { title }))}>
            Create
          </button>
        </Card>
        <Card title={selSurvey ? `Questions (survey ${selSurvey})` : 'Questions'}>
          {!selSurvey && <p className="muted">Select a survey title above.</p>}
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
                New question <input value={qtext} onChange={(e) => setQtext(e.target.value)} />
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
                Add
              </button>
            </>
          )}
        </Card>
      </div>
      {selQuestion !== null && (
        <Card title={`Vote & results (question ${selQuestion})`}>
          <ul className="clean">
            {options.map((o) => (
              <li key={o.id}>
                <span>{o.label}</span>
                {token && (
                  <button onClick={() => vote(selQuestion, o.id)}>Vote</button>
                )}
              </li>
            ))}
          </ul>
          <h4>Tally</h4>
          <ul className="clean">
            {tally.map((t) => (
              <li key={t.option_id}>
                <span>
                  {t.label}: <strong>{t.votes}</strong>
                </span>
              </li>
            ))}
          </ul>
          <label className="field">
            New option <input value={olabel} onChange={(e) => setOlabel(e.target.value)} />
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
            Add option
          </button>
        </Card>
      )}
    </div>
  );
}
