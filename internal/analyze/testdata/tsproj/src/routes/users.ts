import { Router } from 'express';
import { formatName, type Greeting } from '@/lib/format';

export const userRouter = Router();

userRouter.get('/:id', getUser);
userRouter.post('/', createUser);

function getUser(req: any, res: any) {
  const g: Greeting = { text: formatName(req.params.id) };
  res.json(g);
}

/** Creates a user. Returns 201. */
export function createUser(req: any, res: any) {
  res.status(201).end();
}
