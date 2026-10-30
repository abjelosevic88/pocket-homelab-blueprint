import express from 'express';
import { userRouter } from '@/routes/users';
import { formatName } from './lib/format';

const app = express();

app.get('/health', (req, res) => res.send(formatName('ok')));
app.use('/api/users', userRouter);

const port = process.env.PORT || '3000';
app.listen(port);
